/*
Package periodic schedules a task function to run repeatedly at a fixed
interval, with optional random jitter and a per-invocation context timeout.

[New] constructs a [Periodic] scheduler from an interval, a jitter ceiling, a
per-call timeout, and a [TaskFn]. [Periodic.Start] runs the task in a dedicated
goroutine and [Periodic.Stop] shuts it down:

	p, err := periodic.New(
		30*time.Second,  // run every 30 s
		5*time.Second,   // add up to 5 s of random jitter
		10*time.Second,  // each call gets a 10 s deadline
		myTask,
	)
	if err != nil {
		log.Fatal(err)
	}

	p.Start(ctx)
	defer p.Stop()

# Behavior

  - Fixed interval with random jitter: the pause between calls is
    interval + rand(0, jitter), spreading steady-state load across a fleet to
    avoid a thundering herd
    (https://en.wikipedia.org/wiki/Thundering_herd_problem).
  - Per-call timeout: each [TaskFn] invocation receives a [context.Context]
    derived from the parent with an independent deadline.
  - Context-aware shutdown: [Periodic.Start] accepts a parent context;
    canceling it (or calling [Periodic.Stop]) stops the loop after the
    current task invocation returns, without leaking a goroutine.
  - Eager first execution: by default the first call fires after ~1 ns so the
    task runs immediately on start rather than waiting for the first full
    interval. This first call is not jittered, so a fleet that starts in
    lockstep (a rolling deploy, a simultaneous restart) fires every replica's
    first call together; pass [WithInitialJitter] to spread the first call
    across [0, jitter) as well.

# Constraints

  - interval must be > 0.
  - jitter must be >= 0 (pass 0 to disable jitter entirely).
  - timeout must be > 0.
  - task must not be nil.
  - task must not panic: it runs in the background goroutine with no recovery,
    so a panic crashes the process (recover inside the task if needed).

# When To Use

  - A background job refreshes a cache, prunes a table, or emits a heartbeat
    on a schedule.
  - Multiple replicas run the same job and should not all fire at the same
    instant.
  - A single slow run must not stall every subsequent run.
*/
package periodic

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tecnickcom/nurago/pkg/backoff"
)

// TaskFn is the function signature executed on each scheduler tick.
// The context passed in carries the deadline configured via the timeout
// parameter of [New]. Implementations should respect context cancellation
// and return promptly when ctx.Done() is closed.
//
// The task runs in the scheduler's background goroutine with no panic recovery:
// a panic in the task crashes the process. Recover inside the task if it may
// panic and the scheduler must survive.
type TaskFn func(context.Context)

// Periodic schedules a [TaskFn] to run repeatedly with configurable interval and jitter.
// Create one with [New], start it with [Periodic.Start], and stop it with
// [Periodic.Stop]. The zero value is not usable; always use [New].
type Periodic struct {
	interval    time.Duration // Time between two successive calls.
	jitter      time.Duration // Maximum random jitter added between each function call.
	timeout     time.Duration // Timeout applied to each function call via context.
	task        TaskFn        // Function to be periodically executed. It should return within the context's timeout.
	jitterFirst bool          // When set, the first invocation is jittered too; see WithInitialJitter.
	timer       *time.Timer   // Owned solely by the loop goroutine.
	mu          sync.Mutex    // Guards stopped/cancel/done against concurrent or repeated Start/Stop.
	stopped     bool          // set by Stop; a later Start becomes a no-op.
	cancel      context.CancelFunc
	done        chan struct{} // closed by loop on exit so Stop can wait for the in-flight task.
}

// New constructs a Periodic scheduler with constraints on interval, jitter, timeout, and task validation.
// Optional [Option] values configure non-default behavior (see [WithInitialJitter]).
// Returns error if any parameter violates its constraint; call Start() to begin execution.
func New(interval time.Duration, jitter time.Duration, timeout time.Duration, task TaskFn, opts ...Option) (*Periodic, error) {
	if interval < 1 {
		return nil, errors.New("interval must be positive")
	}

	if jitter < 0 {
		return nil, errors.New("jitter must not be negative")
	}

	if timeout < 1 {
		return nil, errors.New("timeout must be positive")
	}

	if task == nil {
		return nil, errors.New("nil task")
	}

	p := &Periodic{
		interval: interval,
		jitter:   jitter,
		timeout:  timeout,
		task:     task,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p, nil
}

// Start begins periodic task execution in a background goroutine.
// The first invocation fires almost immediately by default (or after up to jitter
// when constructed with [WithInitialJitter]); subsequent calls are at
// interval+rand(0,jitter) after each completion.
// The loop exits when ctx is canceled or Stop() is called. A second Start on an
// already-started or already-stopped instance is a no-op, so it never leaks a
// goroutine; the instance is single-use.
//
// If ctx is canceled just before the first tick, the task may still run once
// (with the already-canceled context); a well-behaved task returns promptly.
func (p *Periodic) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancel != nil || p.stopped {
		return // already started, or stopped before starting; either way a no-op
	}

	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})

	go p.loop(ctx)
}

// Stop cancels the execution loop and waits for the current task invocation to complete.
// Safe to call multiple times, before Start(), or concurrently with Start(): whatever
// the ordering, once Stop returns the scheduler is stopped and will not (re)start.
// A Stop that wins the race before Start prevents the loop from ever launching.
// The instance is single-use and cannot be restarted after Stop.
func (p *Periodic) Stop() {
	p.mu.Lock()
	p.stopped = true
	cancel := p.cancel
	done := p.done
	p.mu.Unlock()

	if cancel == nil {
		return
	}

	cancel()
	<-done
}

// loop runs the main periodic execution loop.
func (p *Periodic) loop(ctx context.Context) {
	defer close(p.done)
	defer p.cancel()

	p.timer = time.NewTimer(p.firstDelay())
	defer p.timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.timer.C:
			p.run(ctx)
		}
	}
}

// run executes the task function with a timeout context and schedules the next invocation.
func (p *Periodic) run(ctx context.Context) {
	tctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	p.task(tctx)

	// The timer just fired and was drained by loop's receive; on Go 1.23+ Reset
	// re-arms it with no stale-value risk, so no Stop/drain dance is needed.
	p.timer.Reset(p.nextDelay())
}

// firstDelay returns the delay before the first invocation: one nanosecond
// (eager) by default, or a uniform random value in [0, jitter) when
// [WithInitialJitter] is set and jitter is positive.
func (p *Periodic) firstDelay() time.Duration {
	if !p.jitterFirst || p.jitter <= 0 {
		return time.Nanosecond
	}

	return backoff.AddJitter(0, p.jitter)
}

// nextDelay returns the pause before the next invocation: the fixed interval
// plus uniform random jitter in [0, jitter).
func (p *Periodic) nextDelay() time.Duration {
	return backoff.AddJitter(p.interval, p.jitter)
}
