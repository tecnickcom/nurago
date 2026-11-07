package healthrun

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"
)

// Runner runs a fixed set of checks in parallel.
//
// Each run spawns one goroutine per registered check, so the number of
// in-flight goroutines is (concurrent runs × checks); keep the check set
// bounded and the checks reasonably fast.
type Runner struct {
	checks  []HealthCheck
	logger  *slog.Logger
	timeout time.Duration
}

// NewRunner builds a [Runner] for checks. The provided slice is copied, so
// later caller mutations do not affect the runner. Misconfigured checks (empty
// or duplicate IDs, nil checkers) are logged as warnings.
func NewRunner(checks []HealthCheck, opts ...Option) *Runner {
	r := &Runner{
		checks: append([]HealthCheck(nil), checks...),
		logger: slog.Default(),
	}

	for _, apply := range opts {
		apply(r)
	}

	// Normalize a nil logger (for example from WithLogger(nil)) to slog.Default()
	// so warning and recovered-panic logging never dereference a nil pointer.
	if r.logger == nil {
		r.logger = slog.Default()
	}

	warnInvalidChecks(r.logger, r.checks)

	return r
}

// warnInvalidChecks logs a warning for misconfigured registrations: empty or
// duplicate IDs (which collapse into a single entry in the outcome map) and nil
// checkers (which always fail with [ErrNoChecker]). Every check is still
// registered; the warnings surface the problem at startup.
func warnInvalidChecks(logger *slog.Logger, checks []HealthCheck) {
	seen := make(map[string]struct{}, len(checks))

	for _, hc := range checks {
		if hc.ID == "" {
			logger.Warn("healthcheck registered with an empty ID")
		} else if _, dup := seen[hc.ID]; dup {
			logger.Warn("healthcheck registered with a duplicate ID", slog.String("id", hc.ID))
		}

		if hc.Checker == nil {
			logger.Warn("healthcheck registered with a nil checker", slog.String("id", hc.ID))
		}

		seen[hc.ID] = struct{}{}
	}
}

// Run executes all configured checks in parallel and returns their outcomes
// keyed by check ID: nil for a passing check, otherwise its error.
//
// When several checks share an ID, the entry holds the error of the last
// failing one, or nil when all of them pass. When [WithTimeout] is set, checks
// that do not return in time are reported with [ErrCheckTimeout], as are the
// checks still running when ctx is done.
func (r *Runner) Run(ctx context.Context) map[string]error {
	if r.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	out := make(map[string]error, len(r.checks))

	for _, res := range r.runChecks(ctx) {
		if res.err != nil {
			out[res.id] = res.err

			continue
		}

		// Do not overwrite a failure recorded under the same ID with a pass.
		if _, recorded := out[res.id]; !recorded {
			out[res.id] = nil
		}
	}

	return out
}

// checkResult is the outcome of a single check, kept in registration order.
type checkResult struct {
	id  string
	err error
}

// indexedResult carries a check outcome back to the run goroutine together
// with the slot it belongs to, so only that goroutine writes the results slice.
type indexedResult struct {
	index int
	err   error
}

// runChecks launches every check concurrently and collects their results in
// registration order. Checks that do not report before ctx is done keep their
// pre-seeded [ErrCheckTimeout] outcome.
func (r *Runner) runChecks(ctx context.Context) []checkResult {
	n := len(r.checks)

	results := make([]checkResult, n)
	for i := range results {
		results[i] = checkResult{id: r.checks[i].ID, err: ErrCheckTimeout}
	}

	// Buffered to the number of checks so a check can always deliver its result
	// without blocking, even after collection has stopped on timeout.
	resCh := make(chan indexedResult, n)

	for i, hc := range r.checks {
		go r.runCheck(ctx, i, hc, resCh)
	}

	for remaining := n; remaining > 0; remaining-- {
		select {
		case res := <-resCh:
			results[res.index].err = res.err
		case <-ctx.Done():
			// On deadline or cancellation, still record any results that already
			// arrived, so checks that completed in time are not misreported as
			// timed out. Checks that have not reported keep their seeded outcome.
			drainResults(results, resCh)

			return results
		}
	}

	return results
}

// drainResults records every result currently buffered in resCh without
// blocking, leaving not-yet-reported checks with their pre-seeded outcome.
func drainResults(results []checkResult, resCh <-chan indexedResult) {
	for {
		select {
		case res := <-resCh:
			results[res.index].err = res.err
		default:
			return
		}
	}
}

// runCheck executes a single check and reports its outcome on resCh, converting
// a panic into a regular failure. An unrecovered panic in this child goroutine
// would crash the whole process.
func (r *Runner) runCheck(ctx context.Context, index int, hc HealthCheck, resCh chan<- indexedResult) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.ErrorContext(ctx, "healthcheck checker panicked",
				slog.String("id", hc.ID),
				slog.Any("panic", p),
				slog.String("stack", string(debug.Stack())),
			)

			resCh <- indexedResult{index: index, err: ErrCheckPanic}
		}
	}()

	// Guard a nil checker so it fails cleanly instead of triggering (and logging)
	// a recovered nil-pointer panic on every run.
	if hc.Checker == nil {
		resCh <- indexedResult{index: index, err: ErrNoChecker}

		return
	}

	resCh <- indexedResult{index: index, err: hc.Checker.HealthCheck(ctx)}
}
