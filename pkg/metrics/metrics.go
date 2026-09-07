/*
Package metrics defines a backend-agnostic instrumentation contract for Go
services.

The [Client] interface is an abstraction over common instrumentation points
used across services:

  - SQL opening and DB instrumentation
  - inbound HTTP handler instrumentation
  - outbound HTTP round-tripper instrumentation
  - metrics endpoint handler
  - application-level counters (log levels and error taxonomy)

The [Default] implementation is minimal:

  - it behaves as a no-op for instrumentation methods,
  - it still returns a valid SQL connection from [Default.SqlOpen], and
  - it exposes a health-style metrics handler that returns "OK".

This makes instrumentation optional and allows applications to run without a
configured metrics backend while keeping a consistent API.

# Backends

Concrete implementations are available in sibling packages:
  - github.com/tecnickcom/nurago/pkg/metrics/statsd
  - github.com/tecnickcom/nurago/pkg/metrics/prometheus
  - github.com/tecnickcom/nurago/pkg/metrics/opentel

# When To Use

  - You want to swap Prometheus for OpenTelemetry without touching handler
    code.
  - Tests should run against a no-op implementation rather than a live
    backend.
  - Library code needs to emit metrics without dictating the backend to its
    callers.
*/
package metrics

import (
	"database/sql"
	"net/http"
)

// Client defines the instrumentation surface consumed by the rest of the
// application.
//
// Implementations may export metrics to Prometheus, StatsD, OpenTelemetry, or
// any custom backend, while callers stay decoupled from backend-specific APIs.
type Client interface {
	// SqlOpen wraps sql.Open and may add driver-level instrumentation.
	//
	// Implementations that do not support SQL instrumentation should still
	// return a valid *sql.DB using the provided driverName and dsn.
	SqlOpen(driverName, dsn string) (*sql.DB, error)

	// InstrumentDB attaches instrumentation to an existing DB instance.
	//
	// dbName is the logical name used as a metrics label.
	InstrumentDB(dbName string, db *sql.DB) error

	// InstrumentHandler wraps an inbound HTTP handler to collect request metrics
	// (for example latency, status code, and request counts).
	//
	// path is used as a metrics label (or metric-name segment) and MUST be a
	// low-cardinality route template (for example "/users/{id}"), never a raw
	// request URI containing identifiers. Passing high-cardinality paths causes
	// unbounded label/series growth in the backend.
	InstrumentHandler(path string, handler http.HandlerFunc) http.Handler

	// InstrumentRoundTripper wraps an outbound HTTP transport to collect client
	// request metrics.
	InstrumentRoundTripper(next http.RoundTripper) http.RoundTripper

	// MetricsHandlerFunc returns the HTTP handler for the metrics endpoint.
	//
	// The response contract depends on the backend:
	//   - pull-based backends (Prometheus) return the scrape payload with 200 OK;
	//   - push-based backends expose no scrape payload and return a fixed
	//     response instead: a health-style 200 "OK" (opentel, Default) or
	//     501 Not Implemented (statsd).
	// Callers must not assume the endpoint always exposes a scrape body.
	MetricsHandlerFunc() http.HandlerFunc

	// IncLogLevelCounter increments a counter by log severity level.
	//
	// This is a context-free instrumentation point by design: it is typically
	// driven by a logging hook that has no request context to propagate, so
	// implementations do not correlate the increment with a trace.
	IncLogLevelCounter(level string)

	// IncErrorCounter increments an application error counter partitioned by
	// task, operation, and code labels.
	//
	// Like [Client.IncLogLevelCounter], this is context-free by design; the
	// task/operation/code labels must be low-cardinality values.
	IncErrorCounter(task, operation, code string)

	// Close flushes or tears down backend resources.
	//
	// It should be safe to call during service shutdown.
	Close() error
}

// Default is a no-op implementation of [Client].
//
// It is useful as a safe fallback when no metrics backend is configured,
// allowing instrumentation calls to remain in place without side effects.
type Default struct{}

// SqlOpen opens a database connection, optionally with driver-level instrumentation.
func (c *Default) SqlOpen(driverName, dsn string) (*sql.DB, error) {
	return sql.Open(driverName, dsn) //nolint:wrapcheck
}

// InstrumentDB attaches instrumentation to an existing DB instance (no-op in Default).
func (c *Default) InstrumentDB(_ string, _ *sql.DB) error {
	return nil
}

// InstrumentHandler wraps an inbound handler to collect request metrics, returning handler unchanged in Default.
func (c *Default) InstrumentHandler(_ string, handler http.HandlerFunc) http.Handler {
	return handler
}

// InstrumentRoundTripper wraps an outbound transport to collect request metrics, returning next unchanged in Default.
func (c *Default) InstrumentRoundTripper(next http.RoundTripper) http.RoundTripper {
	return next
}

// MetricsHandlerFunc returns the HTTP handler for the /metrics endpoint or equivalent.
func (c *Default) MetricsHandlerFunc() http.HandlerFunc {
	// Returns "OK" by default.
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`OK`)) }
}

// IncLogLevelCounter increments a counter by log severity level (no-op in Default).
func (c *Default) IncLogLevelCounter(_ string) {
	// Intentionally a no-op: Default records no metrics.
}

// IncErrorCounter increments an application error counter partitioned by task, operation, and code (no-op in Default).
func (c *Default) IncErrorCounter(_, _, _ string) {
	// Intentionally a no-op: Default records no metrics.
}

// Close flushes or tears down backend resources (no-op in Default).
func (c *Default) Close() error {
	return nil
}
