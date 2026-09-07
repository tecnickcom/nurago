/*
Package jsendx implements an extended JSend response envelope for HTTP APIs.

The package wraps payloads in a [Response] schema that extends JSend with runtime
metadata:
  - program name, version, and release
  - RFC3339 datetime and Unix-nanosecond timestamp
  - HTTP code/message and JSend status projection
  - arbitrary response data payload

The [JSXResp] helper integrates with [github.com/tecnickcom/nurago/pkg/httputil]
to send wrapped JSON responses and provides default handlers for common service
endpoints and router fallbacks (not-found, method-not-allowed, panic, index,
ping, status, and public-IP), plus a [healthcheck.ResultWriter] adapter via
[JSXResp.HealthCheckResultWriter].

# When To Use

  - An API contract specifies the JSend format for success, failure, and
    error responses.
  - You want every handler in a service to answer with the same envelope
    shape.
*/
package jsendx

import (
	"context"
	"net/http"
	"time"

	"github.com/tecnickcom/nurago/pkg/healthcheck"
	"github.com/tecnickcom/nurago/pkg/httpserver"
	"github.com/tecnickcom/nurago/pkg/httputil"
)

const (
	// okMessage is the default message for successful responses.
	okMessage = "OK"
)

// Response wraps payload data in a JSend-compatible response envelope.
type Response struct {
	// Program is the application name.
	Program string `json:"program"`

	// Version is the program semantic version (e.g. 1.2.3).
	Version string `json:"version"`

	// Release is the program build number that is appended to the version.
	Release string `json:"release"`

	// DateTime is the human-readable date and time when the response is sent.
	DateTime string `json:"datetime"`

	// Timestamp is the machine-readable UTC timestamp in nanoseconds since EPOCH.
	Timestamp int64 `json:"timestamp"`

	// Status code string (i.e.: error, fail, success).
	Status httputil.Status `json:"status"`

	// Code is the HTTP status code number.
	Code int `json:"code"`

	// Message is the error or general HTTP status message.
	Message string `json:"message"`

	// Data is the content payload.
	Data any `json:"data"`
}

// AppInfo contains application metadata used to enrich JSendX responses.
type AppInfo struct {
	// ProgramName is the application name.
	ProgramName string
	// ProgramVersion is the application semantic version.
	ProgramVersion string
	// ProgramRelease is the application build/release identifier.
	ProgramRelease string
}

// Wrap builds a [Response] for statusCode using info metadata and data payload.
//
// A nil info is treated as an empty [AppInfo] so the response is still produced
// (with empty program metadata) instead of panicking.
func Wrap(statusCode int, info *AppInfo, data any) *Response {
	if info == nil {
		info = &AppInfo{}
	}

	now := time.Now().UTC()

	return &Response{
		Program:   info.ProgramName,
		Version:   info.ProgramVersion,
		Release:   info.ProgramRelease,
		DateTime:  now.Format(time.RFC3339),
		Timestamp: now.UnixNano(),
		Status:    httputil.Status(statusCode),
		Code:      statusCode,
		Message:   http.StatusText(statusCode),
		Data:      data,
	}
}

// JSXResp holds the configuration for the HTTP response methods.
type JSXResp struct {
	httpResp *httputil.HTTPResp
}

// NewJSXResp returns a new [JSXResp] instance.
//
// A nil h falls back to a default [httputil.HTTPResp] (which logs to slog.Default()).
func NewJSXResp(h *httputil.HTTPResp) *JSXResp {
	if h == nil {
		h = httputil.NewHTTPResp(nil)
	}

	return &JSXResp{
		httpResp: h,
	}
}

// Send writes a JSON response wrapped in the JSendX envelope.
//
// The data payload must be JSON-marshalable; if marshaling fails, the response
// falls back to a plain-text 500 Internal Server Error rather than a JSendX
// envelope (see [httputil.HTTPResp.SendJSON]).
func (jr *JSXResp) Send(ctx context.Context, w http.ResponseWriter, statusCode int, info *AppInfo, data any) {
	jr.httpResp.SendJSON(ctx, w, statusCode, Wrap(statusCode, info, data))
}

// DefaultNotFoundHandlerFunc returns the default handler used when no route matches.
func (jr *JSXResp) DefaultNotFoundHandlerFunc(info *AppInfo) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			jr.Send(r.Context(), w, http.StatusNotFound, info, "invalid endpoint")
		},
	)
}

// DefaultMethodNotAllowedHandlerFunc returns the default handler used when a route exists but the method is not allowed.
func (jr *JSXResp) DefaultMethodNotAllowedHandlerFunc(info *AppInfo) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			jr.Send(r.Context(), w, http.StatusMethodNotAllowed, info, "the request cannot be routed")
		},
	)
}

// DefaultPanicHandlerFunc returns the default handler for recovered panics.
func (jr *JSXResp) DefaultPanicHandlerFunc(info *AppInfo) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			jr.Send(r.Context(), w, http.StatusInternalServerError, info, "internal error")
		},
	)
}

// DefaultIndexHandler returns an index handler that renders routes in JSendX format.
func (jr *JSXResp) DefaultIndexHandler(info *AppInfo) httpserver.IndexHandlerFunc {
	return func(routes []httpserver.Route) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			data := &httpserver.Index{Routes: routes}
			jr.Send(r.Context(), w, http.StatusOK, info, data)
		}
	}
}

// DefaultIPHandler returns a handler that resolves and returns the public IP in JSendX format.
func (jr *JSXResp) DefaultIPHandler(info *AppInfo, fn httpserver.GetPublicIPFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK

		ip, err := fn(r.Context())
		if err != nil {
			status = http.StatusFailedDependency
		}

		jr.Send(r.Context(), w, status, info, ip)
	}
}

// DefaultPingHandler returns a ping handler that responds in JSendX format.
func (jr *JSXResp) DefaultPingHandler(info *AppInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jr.Send(r.Context(), w, http.StatusOK, info, okMessage)
	}
}

// DefaultStatusHandler returns a status handler that responds in JSendX format.
func (jr *JSXResp) DefaultStatusHandler(info *AppInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jr.Send(r.Context(), w, http.StatusOK, info, okMessage)
	}
}

// HealthCheckResultWriter returns a [healthcheck.ResultWriter] that emits JSendX responses.
func (jr *JSXResp) HealthCheckResultWriter(info *AppInfo) healthcheck.ResultWriter {
	return func(ctx context.Context, w http.ResponseWriter, statusCode int, data any) {
		jr.Send(ctx, w, statusCode, info, data)
	}
}
