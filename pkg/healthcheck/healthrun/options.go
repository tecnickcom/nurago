package healthrun

import (
	"log/slog"
	"time"
)

// Option configures a [Runner] instance.
type Option func(r *Runner)

// WithLogger sets the logger used for construction warnings and recovered
// checker panics. A nil logger is replaced by slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(r *Runner) {
		r.logger = logger
	}
}

// WithTimeout bounds the total time a run waits for checks to complete.
//
// The timeout is applied to the context passed to each checker, so
// cancellation-aware checks can abort early. Checks that have not returned when
// the deadline is reached are reported with [ErrCheckTimeout]. A non-positive
// duration disables the timeout, leaving only the run context to bound
// execution.
//
// A checker which ignores context cancellation still runs to completion in its
// own goroutine; the timeout bounds the run latency, not the checker itself.
func WithTimeout(timeout time.Duration) Option {
	return func(r *Runner) {
		r.timeout = timeout
	}
}

// WithoutCheckWarnings disables the construction warnings for misconfigured
// checks, for example when the same checks are already validated by another
// runner.
func WithoutCheckWarnings() Option {
	return func(r *Runner) {
		r.noCheckWarnings = true
	}
}
