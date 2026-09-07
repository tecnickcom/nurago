/*
Package retrier provides a configurable retry engine for executing a task
function with backoff, jitter, and per-attempt timeouts.

The delay schedule and jitter strategies are those of
[github.com/tecnickcom/nurago/pkg/backoff], so one retry policy can be applied
consistently to HTTP and non-HTTP work.

# How It Works

[New] creates a [Retrier] with defaults, or with custom [Option] values.
[Retrier.Run] then executes a [TaskFn] according to configured retry rules:

 1. Execute the task with a per-attempt timeout context.
 2. Evaluate the result with a retry predicate ([RetryIfFn]).
 3. Stop when attempts are exhausted or retry is not required.
 4. Otherwise schedule the next attempt after delay + random jitter.
 5. Increase delay by the configured multiplication factor for successive
    retries.

The run loop always respects parent context cancellation.

# Defaults

  - attempts: [DefaultAttempts] (4)
  - initial delay: [DefaultDelay] (1s)
  - delay factor: [DefaultDelayFactor] (2)
  - jitter: [DefaultJitter] (1ms)
  - per-attempt timeout: [DefaultTimeout] (1s)
  - maximum delay: unbounded (use [WithMaxDelay] to cap the pre-jitter backoff)
  - retry condition: [DefaultRetryIf] (retry on any non-nil error)

# Usage

	r, err := retrier.New(
	    retrier.WithAttempts(5),
	    retrier.WithDelay(200*time.Millisecond),
	    retrier.WithDelayFactor(2),
	    retrier.WithJitter(25*time.Millisecond),
	)
	if err != nil {
	    return err
	}

	err = r.Run(ctx, func(ctx context.Context) error {
	    return callExternalService(ctx)
	})
	if err != nil {
	    return err
	}

# When To Use

  - A task talks to a flaky dependency and should be retried on a subset of
    errors.
  - Each attempt needs its own timeout, separate from the overall deadline.
  - You want retry decisions driven by a predicate on the returned error.
*/
package retrier

import (
	"context"
	"fmt"
	"time"

	"github.com/tecnickcom/nurago/pkg/backoff"
)

const (
	// DefaultAttempts is the default maximum number of total attempts (the
	// initial call plus retries).
	DefaultAttempts = 4

	// DefaultDelay is the default delay to apply after the first failed attempt.
	DefaultDelay = 1 * time.Second

	// DefaultDelayFactor is the default multiplication factor to get the successive delay value.
	DefaultDelayFactor = 2

	// DefaultJitter is the default maximum random Jitter time between retries.
	DefaultJitter = 1 * time.Millisecond

	// DefaultTimeout is the default timeout applied to each function call via context.
	DefaultTimeout = 1 * time.Second
)

// TaskFn is the type of function to be executed.
type TaskFn func(ctx context.Context) error

// RetryIfFn is the signature of the function used to decide when retry.
// It must not panic; it runs inline in [Retrier.Run].
type RetryIfFn func(err error) bool

// OnRetryFn is an optional observability callback invoked before each scheduled
// retry. It receives the number of the attempt that just failed (1-based), the
// delay before the next attempt, and the error that triggered the retry. It
// counts scheduled retries (one may still be preempted by cancellation before it
// runs) and must not panic; it runs inline in [Retrier.Run].
type OnRetryFn func(attempt uint, delay time.Duration, err error)

// JitterStrategy selects how backoff jitter is applied. See [backoff.JitterStrategy].
type JitterStrategy = backoff.JitterStrategy

// Jitter strategies, re-exported from [backoff] so callers need not import it.
const (
	JitterAdditive = backoff.JitterAdditive // fixed additive ceiling (default)
	JitterFull     = backoff.JitterFull     // rand[0, delay): best decorrelation
	JitterEqual    = backoff.JitterEqual    // delay/2 + rand[0, delay/2)
)

// Retrier applies configurable retry logic to generic task functions.
//
// A configured Retrier is immutable: it holds no per-run state, so a single
// instance is safe to share and to call concurrently from multiple goroutines.
type Retrier struct {
	delayFactor float64
	attempts    uint
	delay       time.Duration
	jitter      time.Duration
	timeout     time.Duration
	maxDelay    time.Duration
	strategy    JitterStrategy
	retryIfFn   RetryIfFn
	onRetry     OnRetryFn
}

// defaultRetrier returns a [Retrier] initialized with package defaults.
func defaultRetrier() *Retrier {
	return &Retrier{
		attempts:    DefaultAttempts,
		delay:       DefaultDelay,
		delayFactor: DefaultDelayFactor,
		jitter:      DefaultJitter,
		timeout:     DefaultTimeout,
		maxDelay:    0,              // unbounded by default; backoff still applies its internal safety cap
		strategy:    JitterAdditive, // preserve the historical additive-jitter behavior
		retryIfFn:   DefaultRetryIf,
	}
}

// DefaultRetryIf is the default retry predicate: returns true if error is non-nil.
func DefaultRetryIf(err error) bool {
	return err != nil
}

// New constructs a Retrier with defaults, applying optional configuration.
func New(opts ...Option) (*Retrier, error) {
	r := defaultRetrier()

	for _, applyOpt := range opts {
		err := applyOpt(r)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

// run holds the mutable per-call state for a single [Retrier.Run] invocation.
//
// Keeping this state local (instead of on the shared [Retrier]) makes the
// configured [Retrier] immutable and safe to call concurrently.
type run struct {
	cfg               *Retrier
	timer             *time.Timer
	sched             *backoff.Schedule
	remainingAttempts uint
}

// Run executes the task with exponential backoff and jitter, respecting parent context cancellation.
//
// An already-canceled context fails fast without running the task. If ctx is
// canceled during or just before the first attempt, the task may still run once
// (with the canceled context); a well-behaved task returns promptly.
func (r *Retrier) Run(ctx context.Context, task TaskFn) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("context ended before first attempt: %w", err)
	}

	st := &run{
		cfg:   r,
		timer: time.NewTimer(1 * time.Nanosecond),
		sched: backoff.New(backoff.Config{
			Base:     r.delay,
			Factor:   r.delayFactor,
			Jitter:   r.jitter,
			MaxDelay: r.maxDelay,
			Strategy: r.strategy,
		}),
		remainingAttempts: r.attempts,
	}
	defer st.timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context ended: %w", ctx.Err())
		case <-st.timer.C:
			stop, err := st.exec(ctx, task)
			if stop {
				return err
			}
		}
	}
}

// exec runs the task with a per-attempt timeout, evaluates the retry predicate, and schedules the next attempt if needed.
// It returns stop=true to end retrying (success, exhausted attempts, or retry not needed) along with the last task error.
func (s *run) exec(ctx context.Context, task TaskFn) (bool, error) {
	tctx, cancel := context.WithTimeout(ctx, s.cfg.timeout)
	defer cancel()

	taskError := task(tctx)

	s.remainingAttempts--
	if s.remainingAttempts == 0 || !s.cfg.retryIfFn(taskError) {
		return true, taskError
	}

	// If the parent context ended while the task ran, stop now: Run's loop will
	// observe cancellation and return the context error. Scheduling here would
	// report (via onRetry) and arm a retry that never executes.
	if ctx.Err() != nil {
		return false, taskError
	}

	delay := s.sched.Next()

	if s.cfg.onRetry != nil {
		s.cfg.onRetry(s.cfg.attempts-s.remainingAttempts, delay, taskError)
	}

	// The timer just fired and was drained by Run's receive; on Go 1.23+ Reset
	// re-arms it with no stale-value risk, so no Stop/drain dance is needed.
	s.timer.Reset(delay)

	return false, taskError
}
