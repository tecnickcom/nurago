package healthcheck

import (
	"log/slog"
	"time"

	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
)

// HandlerOption configures a [Handler] instance.
type HandlerOption func(h *Handler)

// WithResultWriter overrides how healthcheck results are serialized to HTTP output.
//
// Use this to integrate custom response envelopes or content types. Passing a nil
// writer is treated as "unset": the handler falls back to its default JSON writer.
func WithResultWriter(w ResultWriter) HandlerOption {
	return func(h *Handler) {
		h.writeResult = w
	}
}

// WithLogger sets the logger used by the handler and its default result writer.
func WithLogger(logger *slog.Logger) HandlerOption {
	return func(h *Handler) {
		h.logger = logger
	}
}

// WithTimeout bounds the total time the handler waits for checks to complete.
//
// The timeout is applied to the context passed to each checker, so
// cancellation-aware checks can abort early. Checks that have not returned when
// the deadline is reached are reported as failed with [ErrCheckTimeout] and the
// overall status becomes 503. A non-positive duration disables the handler
// timeout, leaving only the request context to bound execution.
//
// A checker which ignores context cancellation still runs to completion in its
// own goroutine; the timeout bounds the response latency, not the checker itself.
func WithTimeout(timeout time.Duration) HandlerOption {
	return func(h *Handler) {
		h.runnerOpts = append(h.runnerOpts, healthrun.WithTimeout(timeout))
	}
}
