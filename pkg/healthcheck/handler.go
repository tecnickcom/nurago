package healthcheck

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
	"github.com/tecnickcom/nurago/pkg/httputil"
)

const (
	// StatusOK is the canonical success value used in per-check results.
	StatusOK = "OK"
)

var (
	// ErrCheckTimeout is reported for a check that did not return before the
	// configured handler timeout (or the request context) elapsed. See
	// [WithTimeout]. It is [healthrun.ErrCheckTimeout].
	ErrCheckTimeout = healthrun.ErrCheckTimeout

	// ErrCheckPanic is reported in place of a checker panic value. The panic and
	// its stack trace are logged through the handler logger instead of being
	// exposed in the HTTP response. It is [healthrun.ErrCheckPanic].
	ErrCheckPanic = healthrun.ErrCheckPanic

	// ErrNoChecker is reported for a registered check whose Checker is nil. It is
	// a configuration error (also warned at construction) surfaced as a plain
	// failure rather than a recovered nil-pointer panic. It is
	// [healthrun.ErrNoChecker].
	ErrNoChecker = healthrun.ErrNoChecker
)

// ResultWriter writes aggregated healthcheck output to an HTTP response.
type ResultWriter func(ctx context.Context, w http.ResponseWriter, statusCode int, data any)

// Handler aggregates and serves healthcheck results over HTTP.
//
// It responds to any HTTP method and spawns one goroutine per registered check.
// The fan-out is per request, so the number of in-flight goroutines is
// (concurrent requests × checks); keep the check set bounded and the checks
// reasonably fast, and avoid probing this endpoint at extreme frequency.
type Handler struct {
	runner      *healthrun.Runner
	runnerOpts  []healthrun.Option // set by the options, used once by NewHandler
	writeResult ResultWriter
	logger      *slog.Logger
}

// NewHandler builds an HTTP healthcheck aggregator handler.
//
// It executes registered checks concurrently and writes a combined response via
// the configured ResultWriter. The provided slice is copied, so later caller
// mutations do not affect the handler.
func NewHandler(checks []HealthCheck, opts ...HandlerOption) *Handler {
	h := &Handler{
		logger: slog.Default(),
	}

	for _, apply := range opts {
		apply(h)
	}

	// Normalize a nil logger (for example from WithLogger(nil)) to slog.Default()
	// so warning and recovered-panic logging never dereference a nil pointer.
	if h.logger == nil {
		h.logger = slog.Default()
	}

	// Build the default result writer after options are applied so that
	// WithLogger affects it, while WithResultWriter still takes precedence.
	if h.writeResult == nil {
		h.writeResult = httputil.NewHTTPResp(h.logger).SendJSON
	}

	h.runner = healthrun.NewRunner(checks, append(h.runnerOpts, healthrun.WithLogger(h.logger))...)
	h.runnerOpts = nil

	return h
}

// ServeHTTP executes all configured checks in parallel and writes aggregated output.
//
// The response status is 200 when all checks pass, otherwise 503. The response
// body maps check IDs to "OK" or an error message; a check with the same ID as
// another is reported as failed if any of them fail. When [WithTimeout] is set,
// checks that do not return in time are reported with [ErrCheckTimeout].
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	results := h.runner.Run(r.Context())

	status := http.StatusOK
	data := make(map[string]string, len(results))

	for id, err := range results {
		if err != nil {
			status = http.StatusServiceUnavailable
			data[id] = err.Error()

			continue
		}

		data[id] = StatusOK
	}

	// Write with the original request context: the response must be produced even
	// when the check context has already timed out.
	h.writeResult(r.Context(), w, status, data)
}

// Run executes all configured checks in parallel and returns their outcomes
// keyed by check ID: nil for a passing check, otherwise its error. It applies
// the same timeout, panic recovery, and nil-checker handling as
// [Handler.ServeHTTP], so other transports can report the same results.
// [healthrun.Runner] provides the same runs without the HTTP dependencies of
// this package.
//
// When several checks share an ID, the entry holds the error of the last
// failing one, or nil when all of them pass. When [WithTimeout] is set, checks
// that do not return in time are reported with [ErrCheckTimeout], as are the
// checks still running when ctx is done.
func (h *Handler) Run(ctx context.Context) map[string]error {
	return h.runner.Run(ctx)
}
