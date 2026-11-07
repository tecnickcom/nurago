package httpserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
)

// Option configures an [HTTPServer] instance.
type Option func(*config) error

// WithRouter replaces the default router used by the httpServer (mostly used for test purposes with a mock router).
// The supplied router is mutated by New (default handlers are installed and
// routes are registered incrementally), so it must not be reused after a
// failed New call.
func WithRouter(r *httprouter.Router) Option {
	return func(cfg *config) error {
		if r == nil {
			return errors.New("router is required")
		}

		cfg.router = r

		return nil
	}
}

// WithServerAddr sets the address the httpServer will bind to.
func WithServerAddr(addr string) Option {
	return func(cfg *config) error {
		if !srvlifecycle.IsValidAddr(addr) {
			return fmt.Errorf("invalid http server address: %s", addr)
		}

		cfg.serverAddr = addr

		return nil
	}
}

// WithRequestTimeout sets a time limit for all routes after which a request receives a 503 Service Unavailable.
// Alternatively a custom timeout handler like http.TimeoutHandler can be added via WithMiddlewareFn().
//
// The timeout is enforced with http.TimeoutHandler, which buffers the whole
// response in memory and does not support streaming (Flusher/Hijacker); exempt
// streaming routes with a negative Route.Timeout (see DisableTimeout).
func WithRequestTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid requestTimeout")
		}

		cfg.requestTimeout = timeout

		return nil
	}
}

// WithServerReadHeaderTimeout sets the read header timeout.
func WithServerReadHeaderTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid serverReadHeaderTimeout")
		}

		cfg.serverReadHeaderTimeout = timeout

		return nil
	}
}

// WithServerReadTimeout sets the read timeout.
func WithServerReadTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid serverReadTimeout")
		}

		cfg.serverReadTimeout = timeout

		return nil
	}
}

// WithServerWriteTimeout sets the write timeout.
// This is a server-wide connection deadline: long-running or streaming
// responses (e.g. /pprof/profile?seconds=N) are cut off when it elapses, even
// for routes exempted from the request timeout with DisableTimeout, so size it
// to the longest response the server must produce.
func WithServerWriteTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid serverWriteTimeout")
		}

		cfg.serverWriteTimeout = timeout

		return nil
	}
}

// WithServerIdleTimeout sets the maximum amount of time to wait for the next
// request when keep-alives are enabled. If zero, there is no idle timeout.
func WithServerIdleTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		if timeout <= 0 {
			return errors.New("invalid serverIdleTimeout")
		}

		cfg.serverIdleTimeout = timeout

		return nil
	}
}

// WithServerMaxHeaderBytes sets the maximum number of bytes the server will
// read parsing a request's headers (including the request line). It does not
// limit the request body size. When not set, the net/http default
// (http.DefaultMaxHeaderBytes, 1 MiB) applies. Lowering it is a standard
// hardening measure for public listeners.
func WithServerMaxHeaderBytes(n int) Option {
	return func(cfg *config) error {
		if n <= 0 {
			return errors.New("invalid serverMaxHeaderBytes")
		}

		cfg.serverMaxHeaderBytes = n

		return nil
	}
}

// WithMaxRequestBodyBytes sets the maximum number of bytes the server will
// read from a request body, complementing WithServerMaxHeaderBytes for
// public-listener hardening. Reads beyond the limit fail with an
// *http.MaxBytesError (see http.MaxBytesHandler), so handlers can detect it
// and respond with 413 Request Entity Too Large. When not set, the request
// body size is unlimited. Per-route limits can be layered on top with
// Route.Middleware.
func WithMaxRequestBodyBytes(n int64) Option {
	return func(cfg *config) error {
		if n <= 0 {
			return errors.New("invalid maxRequestBodyBytes")
		}

		cfg.maxRequestBodyBytes = n

		return nil
	}
}

// WithShutdownTimeout sets the shutdown timeout. New fails when timeout is not
// positive.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, srvlifecycle.WithShutdownTimeout(timeout))

		return nil
	}
}

// WithShutdownWaitGroup sets the shared waiting group to communicate externally when the server is shutdown.
// The counter is incremented when the server starts (StartServer/StartServerCtx)
// and decremented exactly once on shutdown, so Wait returns immediately if
// called before the server has been started. New fails when wg is nil.
func WithShutdownWaitGroup(wg *sync.WaitGroup) Option {
	return func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, srvlifecycle.WithShutdownWaitGroup(wg))

		return nil
	}
}

// WithShutdownSignalChan sets the shared channel used to signal a shutdown.
// When a value is received on the channel the server initiates the shutdown
// process. Close the channel (rather than sending a single value) to wake every
// server that shares it, since a single send is delivered to only one receiver.
// New fails when ch is nil.
func WithShutdownSignalChan(ch chan struct{}) Option {
	return func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, srvlifecycle.WithShutdownSignalChan(ch))

		return nil
	}
}

// WithTLSCertData enables TLS with the given certificate and key data.
// The resulting configuration advertises HTTP/2 and HTTP/1.1 via ALPN, so
// clients can negotiate either protocol.
func WithTLSCertData(pemCert, pemKey []byte) Option {
	return func(cfg *config) error {
		cert, err := tls.X509KeyPair(pemCert, pemKey)
		if err != nil {
			return fmt.Errorf("failed configuring TLS: %w", err)
		}

		cfg.tlsConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
		}

		return nil
	}
}

// WithTLSConfig sets a custom TLS configuration, giving full control over
// certificates, client authentication, and cipher suites. When both this and
// WithTLSCertData are supplied, the last option wins.
// To serve HTTP/2, include "h2" (and "http/1.1") in NextProtos;
// WithTLSCertData does this automatically.
func WithTLSConfig(tlsConfig *tls.Config) Option {
	return func(cfg *config) error {
		if tlsConfig == nil {
			return errors.New("tlsConfig is required")
		}

		cfg.tlsConfig = tlsConfig

		return nil
	}
}

// WithEnableDefaultRoutes sets the default routes to be enabled on the server.
// An unknown route identifier returns ErrUnknownDefaultRoute.
func WithEnableDefaultRoutes(ids ...DefaultRoute) Option {
	return func(cfg *config) error {
		known := allDefaultRoutes()

		for _, id := range ids {
			if !slices.Contains(known, id) {
				return fmt.Errorf("%w: %q", ErrUnknownDefaultRoute, id)
			}
		}

		cfg.defaultEnabledRoutes = ids

		return nil
	}
}

// WithEnableAllDefaultRoutes enables all default routes on the server.
func WithEnableAllDefaultRoutes() Option {
	return func(cfg *config) error {
		cfg.defaultEnabledRoutes = allDefaultRoutes()
		return nil
	}
}

// WithIndexHandlerFunc replaces the index handler.
// The handler receives every bound route except the index route itself.
// The http.HandlerFunc it returns must not be nil.
func WithIndexHandlerFunc(handler IndexHandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("indexHandlerFunc is required")
		}

		cfg.indexHandlerFunc = handler

		return nil
	}
}

// WithIPHandlerFunc replaces the default ip handler function.
func WithIPHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("ipHandlerFunc is required")
		}

		cfg.ipHandlerFunc = handler

		return nil
	}
}

// WithMetricsHandlerFunc replaces the default metrics handler function.
func WithMetricsHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("metricsHandlerFunc is required")
		}

		cfg.metricsHandlerFunc = handler

		return nil
	}
}

// WithPingHandlerFunc replaces the default ping handler function.
func WithPingHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("pingHandlerFunc is required")
		}

		cfg.pingHandlerFunc = handler

		return nil
	}
}

// WithPProfHandlerFunc replaces the default pprof handler function.
func WithPProfHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("pprofHandlerFunc is required")
		}

		cfg.pprofHandlerFunc = handler

		return nil
	}
}

// WithStatusHandlerFunc replaces the default status handler function.
func WithStatusHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("statusHandlerFunc is required")
		}

		cfg.statusHandlerFunc = handler

		return nil
	}
}

// WithTraceIDHeaderName overrides the default trace id header name.
func WithTraceIDHeaderName(name string) Option {
	return func(cfg *config) error {
		if name == "" {
			return errors.New("traceIDHeaderName is required")
		}

		cfg.traceIDHeaderName = name

		return nil
	}
}

// WithRedactFn set the function used to redact HTTP request and response dumps in the logs.
func WithRedactFn(fn RedactFn) Option {
	return func(cfg *config) error {
		if fn == nil {
			return errors.New("redactFn is required")
		}

		cfg.redactFn = fn

		return nil
	}
}

// WithMiddlewareFn adds one or more middleware handler functions to all routes (endpoints).
// These middleware handlers are applied in the provided order after the default ones and before the custom route ones.
// Nil middleware functions are rejected.
func WithMiddlewareFn(fn ...MiddlewareFn) Option {
	return func(cfg *config) error {
		for _, f := range fn {
			if f == nil {
				return errors.New("middlewareFn is required")
			}
		}

		cfg.middleware = append(cfg.middleware, fn...)

		return nil
	}
}

// WithNotFoundHandlerFunc http handler called when no matching route is found.
func WithNotFoundHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("notFoundHandlerFunc is required")
		}

		cfg.notFoundHandlerFunc = handler

		return nil
	}
}

// WithMethodNotAllowedHandlerFunc http handler called when a request cannot be routed.
func WithMethodNotAllowedHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("methodNotAllowedHandlerFunc is required")
		}

		cfg.methodNotAllowedHandlerFunc = handler

		return nil
	}
}

// WithPanicHandlerFunc http handler to handle panics recovered from http handlers.
func WithPanicHandlerFunc(handler http.HandlerFunc) Option {
	return func(cfg *config) error {
		if handler == nil {
			return errors.New("panicHandlerFunc is required")
		}

		cfg.panicHandlerFunc = handler

		return nil
	}
}

// WithoutRouteLogger disables the logger handler for all routes.
func WithoutRouteLogger() Option {
	return func(cfg *config) error {
		cfg.disableRouteLogger = true
		return nil
	}
}

// WithoutNotFoundLogger disables the request logger for the 404 Not Found handler.
// This is useful to avoid noisy logs from scanners probing unknown paths.
func WithoutNotFoundLogger() Option {
	return func(cfg *config) error {
		cfg.disableNotFoundLogger = true
		return nil
	}
}

// WithoutMethodNotAllowedLogger disables the request logger for the 405 Method Not Allowed handler.
func WithoutMethodNotAllowedLogger() Option {
	return func(cfg *config) error {
		cfg.disableMethodNotAllowedLogger = true
		return nil
	}
}

// WithoutDefaultRouteLogger disables the logger handler for the specified default routes.
// An unknown route identifier returns ErrUnknownDefaultRoute.
func WithoutDefaultRouteLogger(routes ...DefaultRoute) Option {
	return func(cfg *config) error {
		known := allDefaultRoutes()

		for _, route := range routes {
			if !slices.Contains(known, route) {
				return fmt.Errorf("%w: %q", ErrUnknownDefaultRoute, route)
			}
		}

		for _, route := range routes {
			cfg.disableDefaultRouteLogger[route] = true
		}

		return nil
	}
}

// WithLogger overrides the default logger.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) error {
		if logger == nil {
			return errors.New("logger is required")
		}

		cfg.logger = logger

		return nil
	}
}
