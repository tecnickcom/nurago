package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"
	"time"

	libhttputil "github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/traceid"
)

// MiddlewareArgs contains extra optional arguments to be passed to the middleware handler function MiddlewareFn.
type MiddlewareArgs struct {
	// Method is the HTTP method (e.g.: GET, POST, PUT, DELETE, ...).
	Method string

	// Path is the URL path.
	Path string

	// Description is the description of the route or a general description for the handler.
	Description string

	// TraceIDHeaderName is the Trace ID header name.
	TraceIDHeaderName string

	// RedactFunc is the function used to redact HTTP request and response dumps in the logs.
	RedactFunc RedactFn

	// Logger is the logger.
	Logger *slog.Logger

	// Rnd is the random generator.
	Rnd *random.Rnd
}

// MiddlewareFn is a function that wraps an http.Handler.
type MiddlewareFn func(args MiddlewareArgs, next http.Handler) http.Handler

// RequestInjectHandler wraps all incoming requests and injects a logger in the request scoped context.
//
// Nil arguments fall back to safe defaults (slog.Default(), a new random
// generator, and the shared redact.Default() redactor), so the returned handler
// never panics on missing dependencies.
//
// The final log entry includes the response status code (response_code, with
// the implicit 200 recorded for handlers that never call WriteHeader) and the
// number of body bytes written (response_size). Hijacked connections (e.g.
// WebSocket upgrades) never write an HTTP status through the writer and are
// therefore logged with the implicit 200 as well.
//
// The writer passed to next forwards http.Flusher, http.Hijacker, http.Pusher,
// io.ReaderFrom, and http.ResponseController (via Unwrap), but not the
// deprecated http.CloseNotifier; use Request.Context() for cancelation instead.
//
// At debug level the whole request (headers and body) is dumped into the log
// entry via httputil.DumpRequest, which buffers the entire body in memory; keep
// this in mind when enabling debug logging for endpoints that accept large bodies.
func RequestInjectHandler(
	logger *slog.Logger,
	traceIDHeaderName string,
	redactFn RedactFn,
	rnd *random.Rnd,
	next http.Handler,
) http.Handler {
	logger, redactFn, rnd = requestInjectDefaults(logger, redactFn, rnd)

	fn := func(w http.ResponseWriter, r *http.Request) {
		// Kept in local time: UTC() would drop the monotonic clock reading
		// that response durations are measured with.
		reqTime := time.Now()

		// Only generate a new trace ID when the request does not carry a valid one.
		reqID := traceid.FromHTTPRequestHeader(r, traceIDHeaderName, "")
		if reqID == "" {
			reqID = rnd.UUIDv7().String()
		}

		ctx := r.Context()
		ctx = libhttputil.WithRequestTime(ctx, reqTime)
		ctx = traceid.ForceContext(ctx, reqID)

		// Derive a per-request logger from the shared one. The captured logger
		// must never be reassigned, otherwise concurrent requests would race on
		// it and cross-attribute log fields.
		reqLogger := logger.With(
			slog.String(traceid.DefaultLogKey, reqID),
			slog.Time("request_time", reqTime.UTC()),
			slog.String("request_method", r.Method),
			slog.String("request_path", r.URL.Path),
			slog.String("request_query", redactFn([]byte(r.URL.RawQuery))),
			slog.String("request_remote_address", r.RemoteAddr),
			slog.String("request_uri", redactRequestURI(r.RequestURI, redactFn)),
			slog.String("request_user_agent", r.UserAgent()),
			slog.String("request_x_forwarded_for", r.Header.Get("X-Forwarded-For")),
		)

		dbglog := reqLogger.Enabled(ctx, slog.LevelDebug)

		if dbglog {
			reqDump, _ := httputil.DumpRequest(r, true)
			reqLogger = reqLogger.With(slog.String("request_dump", redactFn(reqDump)))
		}

		// Track the response status and size so the request log entry carries
		// response metadata even for handlers that write directly to the writer.
		rw := libhttputil.NewResponseWriterWrapper(w)

		next.ServeHTTP(rw, r.WithContext(ctx))

		status := rw.Status()
		if status == 0 {
			// The handler never called WriteHeader: net/http sends an implicit 200.
			status = http.StatusOK
		}

		reqLogger = reqLogger.With(
			slog.Int("response_code", status),
			slog.Int("response_size", rw.Size()),
		)

		if dbglog {
			reqLogger.Debug("request")
			return
		}

		reqLogger.Info("request")
	}

	return http.HandlerFunc(fn)
}

// requestInjectDefaults replaces nil RequestInjectHandler dependencies with
// safe defaults. The redaction fallback must fail safe: it defaults to the
// same redacting function used by defaultConfig, never to an identity function.
func requestInjectDefaults(logger *slog.Logger, redactFn RedactFn, rnd *random.Rnd) (*slog.Logger, RedactFn, *random.Rnd) {
	if logger == nil {
		logger = slog.Default()
	}

	if redactFn == nil {
		redactFn = redact.Default().BytesToString
	}

	if rnd == nil {
		rnd = random.New(nil)
	}

	return logger, redactFn, rnd
}

// redactRequestURI redacts the query-string portion of a raw request target
// (r.RequestURI) so secrets carried in query parameters (for example token or
// api_key) are not written to logs. The path portion is preserved; a target with
// no query is returned unchanged.
func redactRequestURI(uri string, redactFn RedactFn) string {
	q := strings.IndexByte(uri, '?')
	if q < 0 {
		return uri
	}

	return uri[:q+1] + redactFn([]byte(uri[q+1:]))
}

// LoggerMiddlewareFn returns the middleware handler function to handle logs.
func LoggerMiddlewareFn(args MiddlewareArgs, next http.Handler) http.Handler {
	return RequestInjectHandler(args.Logger, args.TraceIDHeaderName, args.RedactFunc, args.Rnd, next)
}

// ApplyMiddleware returns an http Handler with all middleware handler functions applied.
// Nil middleware entries are skipped, so the function is safe to call with
// partially populated middleware lists.
func ApplyMiddleware(arg MiddlewareArgs, next http.Handler, middleware ...MiddlewareFn) http.Handler {
	for _, v := range slices.Backward(middleware) {
		if v == nil {
			continue
		}

		next = v(arg, next)
	}

	return next
}
