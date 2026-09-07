/*
Package bootstrap wires together the core infrastructure of a Go service:
context lifecycle, structured logging, metrics collection, OS signal handling,
and graceful shutdown, in a single function call.

# How It Works

The entry point is [Bootstrap]. It accepts a [BindFunc], the caller-supplied
function that wires up all application-specific components, plus a variadic
list of [Option] values that tune the runtime behavior:

 1. A cancellable [context.Context] is created and threaded through the entire
    application via BindFunc.
 2. A [metrics.Client] is created (Prometheus by default) and passed to BindFunc.
 3. A [*slog.Logger] is created and passed to BindFunc.
    If a [logutil.Config] is provided with [WithLogConfig], the logger
    emits a metrics counter for every log line, broken down by level.
 4. BindFunc is called. This is where the caller registers HTTP servers,
    database connections, background workers, etc.
 5. Bootstrap blocks until it receives os.Interrupt (SIGINT), SIGTERM, or
    until the context is canceled externally.
 6. A shutdown signal is broadcast on the shared channel
    (see [WithShutdownSignalChan]) and the application context is canceled, so
    every registered dependent (whether keyed on the channel or on ctx.Done())
    can start its own teardown.
 7. Bootstrap waits for all dependants to finish via a [sync.WaitGroup]
    (see [WithShutdownWaitGroup]), bounded by a configurable timeout
    (see [WithShutdownTimeout]) to prevent hanging indefinitely. If the timeout
    fires first, Bootstrap returns an error wrapping [ErrShutdownTimeout].
 8. The metrics client is closed so buffered measurements are flushed before
    the process exits.

# Notes

Bootstrap installs process-global OS signal handling via [os/signal.Notify], so
it is intended to be called once per process (for example from main) and not
concurrently with another Bootstrap call in the same process. Supplying a
[logutil.Config] with [WithLogConfig] also replaces the process-wide default
logger (via slog.SetDefault) and redirects the standard library log package.

# Usage

Wire your application in a [BindFunc] and pass it to [Bootstrap]:

	func bind(ctx context.Context, l *slog.Logger, m metrics.Client) error {
	    // register HTTP servers, workers, DB connections, etc.
	    return nil
	}

	func main() {
	    shutdownWG := &sync.WaitGroup{}
	    shutdownCh := make(chan struct{})

	    err := bootstrap.Bootstrap(
	        bind,
	        bootstrap.WithLogConfig(logutil.DefaultConfig()),
	        bootstrap.WithShutdownTimeout(30*time.Second),
	        bootstrap.WithShutdownWaitGroup(shutdownWG),
	        bootstrap.WithShutdownSignalChan(shutdownCh),
	    )
	    if err != nil {
	        log.Fatal(err)
	    }
	}

For a complete, runnable implementation see, in order:
  - examples/service/cmd/main.go
  - examples/service/internal/cli/cli.go
  - examples/service/internal/cli/bind.go

# When To Use

  - A service's main should be short and the lifecycle wiring consistent
    across services.
  - Shutdown must wait for background workers, bounded by a timeout.
  - Log records should increment a metrics counter per level.
*/
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tecnickcom/nurago/pkg/metrics"
)

// Bootstrap initializes core service infrastructure and manages process lifecycle.
//
// It centralizes context setup, logger and metrics creation, application
// binding, signal handling, and graceful termination in one call.
//
// The function applies options, validates configuration, invokes bindFn to wire
// dependants, waits for shutdown signals, broadcasts a shutdown event to all
// listeners, then waits for dependant completion up to the configured timeout.
//
// It returns a wrapped error when configuration is invalid, when metrics or
// application binding fails, or, wrapping [ErrShutdownTimeout], when dependants
// do not finish within the configured shutdown timeout. All returned errors are
// matchable with errors.Is.
func Bootstrap(bindFn BindFunc, opts ...Option) error {
	if bindFn == nil {
		return ErrNilBindFunc
	}

	cfg := defaultConfig()

	for _, applyOpt := range opts {
		applyOpt(cfg)
	}

	err := cfg.validate()
	if err != nil {
		return err
	}

	// create application context
	ctx, cancel := context.WithCancel(cfg.context)
	defer cancel()

	m, err := cfg.createMetricsClientFunc()
	if err != nil {
		return fmt.Errorf("error creating application metric: %w", err)
	}

	// Wire a metric counter for every log line, chained after any caller-installed
	// hook. This works on a shallow copy so the caller-owned logutil.Config is
	// never mutated, and is a no-op when WithLogConfig was not used.
	cfg.logConfig = cfg.logConfigWithMetricHook(m)

	l := cfg.createLoggerFunc()
	if l == nil {
		// A logger factory must never return nil; fall back to the process default
		// so a misconfigured factory can never take the whole service down.
		l = slog.Default()
	}

	// Register signal handling before binding so a termination signal delivered
	// while bindFn is still wiring dependants is retained (quit is buffered) and
	// honored immediately afterwards, rather than hard-killing the process.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// stop relaying OS signals to quit once shutdown handling is done so the
	// registration does not outlive this Bootstrap call.
	defer signal.Stop(quit)

	l.Debug("binding application components")

	err = bindFn(ctx, l, m)
	if err != nil {
		closeMetricsClient(m, l)

		return fmt.Errorf("application bootstrap error: %w", err)
	}

	l.Info("application started")

	waitForShutdown(ctx, quit, l)
	l.Info("application stopping")

	// Send shutdown signal to all dependants (e.g. HTTP servers) by closing the
	// broadcast channel. The channel must be single-use: Bootstrap closes it
	// exactly once per call, so a caller-supplied channel (see
	// WithShutdownSignalChan) must not be shared across Bootstrap invocations or
	// closed elsewhere, otherwise this close panics.
	close(cfg.shutdownSignalChan)

	// Cancel the application context together with the shutdown broadcast, so
	// dependants whose teardown is keyed on ctx.Done() observe the cancellation
	// before Bootstrap starts waiting on the shutdown WaitGroup.
	cancel()

	// wait for graceful shutdown of dependants
	completed := syncWaitGroupTimeout(cfg.shutdownWaitGroup, cfg.shutdownTimeout, l)

	// flush and release the metrics backend resources
	closeMetricsClient(m, l)

	l.Info("application stopped")

	if !completed {
		return fmt.Errorf("shutdown exceeded %s: %w", cfg.shutdownTimeout, ErrShutdownTimeout)
	}

	return nil
}

// waitForShutdown blocks until an OS termination signal is received on quit or the
// application context is canceled, logging which trigger fired.
//
// Context cancellation is a normal, expected shutdown trigger (for example a parent
// context or signal.NotifyContext), so it is logged at Info rather than Warn to
// avoid inflating error-rate metrics derived from the log-level hook.
func waitForShutdown(ctx context.Context, quit <-chan os.Signal, l *slog.Logger) {
	select {
	case <-quit:
		l.Debug("shutdown signal received")
	case <-ctx.Done():
		l.Info("context canceled")
	}
}

// closeMetricsClient closes the metrics client, logging any close error.
//
// Closing flushes buffered measurements (e.g. statsd buffers, OTel readers) so
// the last metrics emitted before shutdown are not silently dropped.
func closeMetricsClient(m metrics.Client, l *slog.Logger) {
	err := m.Close()
	if err != nil {
		l.Error("error closing the metrics client", slog.Any("error", err))
	}
}

// syncWaitGroupTimeout waits for wg completion with an upper time bound, returning
// true if wg completed and false if the timeout fired first.
//
// It prevents shutdown from hanging forever when a dependant goroutine fails to
// signal completion, and logs whether shutdown completed normally or timed out.
//
// NOTE: sync.WaitGroup offers no cancellable wait, so when the timeout fires
// the internal goroutine stays blocked in wg.Wait() until every dependant
// eventually calls Done. If a dependant never does, that goroutine leaks for
// the remaining process lifetime. This is an accepted trade-off: the typical
// caller exits right after Bootstrap returns, and the alternative would change
// the public WaitGroup-based dependant contract.
func syncWaitGroupTimeout(wg *sync.WaitGroup, timeout time.Duration, l *slog.Logger) bool {
	wait := make(chan struct{})

	go func() {
		defer close(wait)

		wg.Wait()
	}()

	select {
	case <-wait:
		l.Debug("dependents shutdown complete")

		return true
	case <-time.After(timeout):
		l.Warn("dependents shutdown timeout")

		return false
	}
}
