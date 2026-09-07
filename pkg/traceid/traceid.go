/*
Package traceid captures a request-scoped trace ID at the service boundary: it
reads the ID from an inbound HTTP header, propagates it through the
[context.Context] for the lifetime of the request, and writes it back into
outbound HTTP headers when calling downstream services, without coupling
business logic to any particular tracing framework.

# How It Works

The package uses an unexported struct type (ctxKey) as the context key,
guaranteeing no collision with any other package's context values.

  - [NewContext] stores the trace ID in the context. If an ID is already
    present the context is returned unchanged, making it safe to call at every
    layer without overwriting an upstream-supplied ID.
  - [ForceContext] stores the trace ID unconditionally, overwriting any ID
    already present; use it when the authoritative ID has just been determined
    and the context must agree with what is propagated downstream.
  - [FromContext] retrieves the stored ID, falling back to a caller-supplied
    default (typically [DefaultValue]) when none is set.
  - [FromHTTPRequestHeader] extracts the trace ID from an incoming HTTP request
    header and validates it with [Valid] (1 to [MaxIDLen] characters from the
    set [0-9A-Za-z._-]). Any value that does not match (including an empty or
    absent header) is replaced with the caller-supplied default, preventing
    header-injection.
  - [SetHTTPRequestHeaderFromContext] reads the ID from the context and writes
    it onto an outgoing [*http.Request] header, completing the propagation loop
    to downstream services.

The context stored under the key is not validated (see [NewContext]); callers
that ingest an ID from an untrusted origin should validate it with [Valid], or
obtain it through [FromHTTPRequestHeader], before storing or logging it.

The package also exports the conventional defaults [DefaultHeader] ("X-Request-ID"),
[DefaultValue] (""), and [DefaultLogKey] ("traceid") so all services in a system
can share consistent naming without hardcoding strings.

# Usage

At the inbound boundary (e.g. an HTTP middleware):

	id := traceid.FromHTTPRequestHeader(r, traceid.DefaultHeader, traceid.DefaultValue)
	ctx := traceid.NewContext(r.Context(), id)
	// pass ctx to all downstream handlers

Anywhere in the call chain (logging, metrics, business logic):

	id := traceid.FromContext(ctx, traceid.DefaultValue)
	logger.With(traceid.DefaultLogKey, id).Info("processing request")

At an outbound boundary (e.g. before calling a downstream service):

	traceid.SetHTTPRequestHeaderFromContext(ctx, outboundReq, traceid.DefaultHeader, traceid.DefaultValue)

# When To Use

  - Log lines across a request must share one correlation ID.
  - You want trace propagation without coupling business logic to a tracing
    SDK.
  - Downstream services expect the ID in a specific header.
*/
package traceid

import (
	"context"
	"net/http"
)

const (
	// DefaultHeader is the default header name for the trace ID.
	DefaultHeader = "X-Request-ID"

	// DefaultValue is the default trace ID value.
	DefaultValue = ""

	// DefaultLogKey is the default log field key for the Trace ID.
	DefaultLogKey = "traceid"

	// MaxIDLen is the maximum number of bytes accepted in a valid trace ID.
	MaxIDLen = 64
)

// ctxKey is used to store the trace ID in the context.
type ctxKey struct{}

// Valid reports whether id is a well-formed trace ID: between 1 and [MaxIDLen]
// bytes drawn exclusively from the set [0-9A-Za-z._-]. The set deliberately
// excludes CR, LF, spaces and ':' so that a validated ID is always safe to write
// into an HTTP header value or a structured log field without further escaping.
func Valid(id string) bool {
	if len(id) == 0 || len(id) > MaxIDLen {
		return false
	}

	for i := range len(id) {
		if !isIDByte(id[i]) {
			return false
		}
	}

	return true
}

// isIDByte reports whether c is one of the bytes permitted in a trace ID.
func isIDByte(c byte) bool {
	switch {
	case c >= '0' && c <= '9',
		c >= 'A' && c <= 'Z',
		c >= 'a' && c <= 'z',
		c == '-', c == '_', c == '.':
		return true
	default:
		return false
	}
}

// NewContext stores the trace ID value in the context if not already present, making it safe to call at multiple layers.
// An empty ID is not stored, so that a later real ID is not shadowed by an earlier empty one.
// The ID is stored verbatim and is not validated; validate untrusted input with [Valid] (or obtain it via
// [FromHTTPRequestHeader]) before storing it, because callers may log the value later returned by [FromContext].
func NewContext(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}

	if _, ok := ctx.Value(ctxKey{}).(string); ok {
		return ctx
	}

	return context.WithValue(ctx, ctxKey{}, id)
}

// ForceContext stores the trace ID in the context, overwriting any value that
// may already be present. Unlike [NewContext], which preserves an already-stored
// ID, this guarantees the returned context reflects the supplied ID. It is meant
// for callers that have just determined the authoritative ID (for example after
// replacing an invalid stored ID with a freshly generated one) and need the
// context to agree with what was propagated downstream.
// An empty ID is not stored and the context is returned unchanged; if the stored
// ID already equals id the context is returned unchanged to avoid an
// unnecessary allocation. Like [NewContext], the ID is stored verbatim and is
// not validated.
func ForceContext(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}

	if existing, ok := ctx.Value(ctxKey{}).(string); ok && existing == id {
		return ctx
	}

	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext retrieves the trace ID from context, returning the defaultValue if not found.
// The returned value is whatever was stored (see [NewContext]); it is not re-validated, so pass a value from an
// untrusted origin through [Valid] before logging or otherwise trusting it.
func FromContext(ctx context.Context, defaultValue string) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}

	return defaultValue
}

// SetHTTPRequestHeaderFromContext retrieves the trace ID from context and sets it onto the HTTP request header, validating it first; returns the ID that was set.
// The outbound ID is validated with [Valid] to prevent header injection; any value that does not match is replaced with defaultValue before being written.
// The defaultValue is subject to the same validation: when the final value is empty or invalid no header is written (so no empty or unvalidated header is transmitted) and an empty string is returned; any value already present on the request header is left unchanged.
// If r is nil the function is a no-op and returns an empty string.
func SetHTTPRequestHeaderFromContext(ctx context.Context, r *http.Request, header, defaultValue string) string {
	if r == nil {
		return ""
	}

	id := FromContext(ctx, defaultValue)

	if !Valid(id) {
		id = defaultValue
	}

	// Valid requires at least one character, so this also skips empty values.
	if !Valid(id) {
		return ""
	}

	if r.Header == nil {
		r.Header = make(http.Header)
	}

	r.Header.Set(header, id)

	return id
}

// FromHTTPRequestHeader extracts the trace ID from the HTTP request header and validates it with [Valid]; returns defaultValue if the value is invalid, missing, or r is nil.
func FromHTTPRequestHeader(r *http.Request, header, defaultValue string) string {
	if r == nil {
		return defaultValue
	}

	id := r.Header.Get(header)

	if !Valid(id) {
		return defaultValue
	}

	return id
}
