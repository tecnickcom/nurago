/*
Package httpserver provides a configurable HTTP server bootstrap for Go services.

The package assembles the server from:
  - option-driven configuration ([New] + functional [Option]s)
  - pluggable route binding via [Binder]
  - configurable default route set for operational endpoints
  - shared middleware application model
  - graceful shutdown orchestration via context and/or signal channel

Custom service routes are supplied by a [Binder], while default routes can be
enabled selectively (or all at once) through options.

# Default Operational Routes

When enabled, the built-in route set includes:
  - /ip: returns the service public IP (via ipify integration)
  - /metrics: returns metrics payload (501 by default unless replaced)
  - /ping: liveness endpoint
  - /pprof/*option: pprof profiling endpoints
  - /status: service health endpoint
  - / (index): generated route index

# Behavior

  - Lifecycle: non-blocking start, context-aware shutdown, configurable
    shutdown timeout, external wait-group and signal integration,
    abnormal-termination reporting via [HTTPServer.ServeError], and ephemeral
    port discovery via [HTTPServer.Addr].
  - Startup validation: misconfigured routes and options (nil handlers or
    middleware, duplicate or malformed routes, unknown default route
    identifiers) are reported by [New] as wrapped sentinel errors instead of
    panics.
  - Router defaults: not-found, method-not-allowed, and panic handlers with
    structured logging.
  - Middleware pipeline: common middleware (logger/timeout) plus global and
    per-route middleware composition, with per-route timeout override or
    opt-out ([DisableTimeout]).
  - Observability: trace-id propagation hooks, HTTP data redaction, per-request
    log entries carrying the response status code and size, optional
    pprof/metrics/status routes, and net/http internal diagnostics routed to
    the structured logger.
  - Transport: plain TCP or TLS (HTTP/1.1 and HTTP/2 via ALPN) from cert/key
    material ([WithTLSCertData]) or a custom [WithTLSConfig].

# Security

The default operational routes expose service internals: /pprof/*option serves
runtime profiles (memory layout, goroutine stacks, CPU traces), the index route
enumerates every registered endpoint, /metrics may reveal implementation
details, and /ip performs an outbound call to a third-party service. Enable
these routes only on internal or administrative listeners that are not
reachable from the public internet, or protect them with authentication
middleware appropriate for your environment.

For a usage example, refer to examples/service/internal/cli/bind.go.

# When To Use

  - A service needs a server with sane timeouts, health endpoints, and pprof
    mounted correctly.
  - Shutdown must drain in-flight requests before the process exits.
  - You want the routing to stay httprouter, not a framework.
*/
package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/logutil"
)

// Binder is an interface to allow configuring the HTTP router.
type Binder interface {
	// BindHTTP returns the routes.
	BindHTTP(ctx context.Context) []Route
}

// NopBinder returns no-operation binder that supplies no custom routes to router.
func NopBinder() Binder {
	return &nopBinder{}
}

// nopBinder is a no-operation binder implementation.
type nopBinder struct{}

// BindHTTP implements the Binder interface.
func (b *nopBinder) BindHTTP(_ context.Context) []Route { return nil }

// HTTPServer defines the HTTP Server object.
type HTTPServer struct {
	cfg          *config
	ctx          context.Context //nolint:containedctx
	httpServer   *http.Server
	listener     net.Listener
	startedMutex sync.Mutex
	started      bool
	stopped      bool // set once Shutdown has run, guarding against a late Start.
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	monitorDone  chan struct{} // closed when the shutdown-monitor goroutine exits
	serveErr     chan error    // buffered (size 1); receives an abnormal Serve failure
}

// New constructs HTTP server with router, middleware, default operational routes, TLS, and graceful shutdown orchestration.
//
// The listener is bound immediately: on success the server already holds the
// network address. If the returned server is never started, call [HTTPServer.Shutdown]
// to release the bound listener. A nil ctx is treated as context.Background().
//
// Note: the underlying httprouter may emit trailing-slash and fixed-path
// redirects (HTTP 301) and automatic OPTIONS responses before the middleware
// pipeline runs; those are not passed through the request logger. Supply a
// custom router via [WithRouter] to change that behavior.
//
//nolint:contextcheck // a nil ctx is deliberately replaced with context.Background()
func New(ctx context.Context, binder Binder, opts ...Option) (*HTTPServer, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if binder == nil {
		return nil, ErrNilBinder
	}

	cfg := defaultConfig()

	for _, applyOpt := range opts {
		err := applyOpt(cfg)
		if err != nil {
			return nil, err
		}
	}

	cfg.logger = cfg.logger.With(
		slog.String("component", "httpserver"),
		slog.String("addr", cfg.serverAddr),
	)

	cfg.httpresp = httputil.NewHTTPResp(cfg.logger)

	cfg.setRouter()

	err := loadRoutes(ctx, binder, cfg)
	if err != nil {
		return nil, err
	}

	listener, err := netListener(ctx, cfg.serverAddr, cfg.tlsConfig)
	if err != nil {
		return nil, err
	}

	// Request contexts inherit the values of the application context but not
	// its cancelation: canceling the application context triggers a graceful
	// shutdown, and in-flight requests must be allowed to complete within the
	// shutdown grace period instead of being canceled immediately.
	baseCtx := context.WithoutCancel(ctx)

	// Pin the accepted protocol set explicitly: HTTP/1.1 always, and HTTP/2
	// when the TLS configuration advertises "h2" via ALPN (the listener is
	// created by this package, so ALPN advertisement is controlled solely by
	// the tls.Config NextProtos, not by net/http). This matches the net/http
	// defaults for this configuration and shields the server from future
	// changes to those defaults.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)

	return &HTTPServer{
			cfg: cfg,
			ctx: ctx,
			httpServer: &http.Server{
				Addr:              cfg.serverAddr,
				Handler:           cfg.router,
				ReadHeaderTimeout: cfg.serverReadHeaderTimeout,
				ReadTimeout:       cfg.serverReadTimeout,
				IdleTimeout:       cfg.serverIdleTimeout,
				MaxHeaderBytes:    cfg.serverMaxHeaderBytes,
				TLSConfig:         cfg.tlsConfig,
				WriteTimeout:      cfg.serverWriteTimeout,
				BaseContext:       func(_ net.Listener) context.Context { return baseCtx },
				ErrorLog:          logutil.NewLogFromSlog(cfg.logger),
				Protocols:         protocols,
			},
			listener:     listener,
			shutdownDone: make(chan struct{}),
			monitorDone:  make(chan struct{}),
			serveErr:     make(chan error, 1),
		},
		nil
}

// Addr returns the network address the server listener is bound to. It is
// primarily useful when binding to an ephemeral port (":0") to discover the
// actual port assigned by the operating system.
func (h *HTTPServer) Addr() net.Addr {
	return h.listener.Addr()
}

// ServeError returns a channel that receives the error that caused the server
// to stop serving unexpectedly (any Serve failure other than a normal shutdown).
// The channel is buffered (size 1) and never receives a nil value. A server that
// stops through Shutdown leaves it empty.
func (h *HTTPServer) ServeError() <-chan error {
	return h.serveErr
}

// StartServerCtx starts server in background goroutine with context-aware shutdown support.
//
// The provided ctx controls shutdown only: its cancelation triggers the
// graceful-shutdown sequence. Request-scoped context values are inherited
// from the ctx passed to New, not from this one.
//
// It is a no-op if the server has already been started or has already been shut
// down: this keeps the shutdown wait group balanced (a single Add(1) matched by a
// single decrement) and avoids serving twice on the same listener.
func (h *HTTPServer) StartServerCtx(ctx context.Context) {
	// Register the running server with the wait group synchronously, before any
	// goroutine (including the shutdown one) can decrement it. The matching
	// decrement happens exactly once in Shutdown via sync.Once, and only for a
	// started server (Shutdown on a never-started server must not decrement).
	h.startedMutex.Lock()

	if h.started || h.stopped {
		h.startedMutex.Unlock()
		h.cfg.logger.Warn("start ignored: server already started or shut down")

		return
	}

	h.started = true
	h.cfg.shutdownWaitGroup.Add(1)
	h.startedMutex.Unlock()

	// wait for shutdown signal, direct Shutdown call, or context cancelation
	go func() { //nolint:gosec
		defer close(h.monitorDone)

		select {
		case <-h.cfg.shutdownSignalChan:
			h.cfg.logger.Debug("shutdown notification received")
		case <-h.shutdownDone:
			// Shutdown was already called directly: exit without leaking this goroutine.
			return
		case <-ctx.Done():
			h.cfg.logger.Warn("context canceled")
		}

		// The shutdown context is independent from the parent context.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), h.cfg.shutdownTimeout)
		defer cancel()

		_ = h.Shutdown(shutdownCtx) //nolint:contextcheck
	}()

	// start server
	go func() { //nolint:contextcheck
		h.serve()
	}()

	h.cfg.logger.Info("listening for http requests")
}

// StartServer starts server in background using context from New().
func (h *HTTPServer) StartServer() {
	h.StartServerCtx(h.ctx)
}

// Shutdown gracefully shuts down server with timeout enforcement; wraps net/http Server.Shutdown().
// It is safe to call Shutdown any number of times and from multiple paths
// (e.g. manually and via the internal context/signal goroutine): the wait group
// is decremented exactly once, and only when the server was actually started.
func (h *HTTPServer) Shutdown(ctx context.Context) error {
	h.cfg.logger.Debug("shutting down http server")

	err := h.httpServer.Shutdown(ctx)

	h.shutdownOnce.Do(func() {
		// Unblock the internal shutdown-monitor goroutine (if any),
		// so a direct Shutdown call does not leak it.
		close(h.shutdownDone)

		h.startedMutex.Lock()
		defer h.startedMutex.Unlock()

		h.stopped = true

		if h.started {
			h.cfg.shutdownWaitGroup.Add(-1)
			return
		}

		// http.Server.Shutdown only closes listeners registered by Serve,
		// so a never-started server must release its bound listener explicitly.
		cerr := h.listener.Close()
		if cerr != nil {
			h.cfg.logger.With(slog.Any("error", cerr)).Debug("failed closing the http server listener")
		}
	})

	if err != nil {
		h.cfg.logger.With(slog.Any("error", err)).Warn("http server shutdown completed with error")

		return err //nolint:wrapcheck
	}

	h.cfg.logger.Debug("http server shutdown complete")

	return nil
}

// serve starts serving HTTP requests. On any failure other than a normal
// shutdown it releases the wait group and unblocks the monitor goroutine by
// triggering Shutdown, and surfaces the error on the ServeError channel so the
// application is not left waiting on a server that has already died.
func (h *HTTPServer) serve() {
	err := h.httpServer.Serve(h.listener)
	if errors.Is(err, http.ErrServerClosed) {
		h.cfg.logger.Debug("closed http server")
		return
	}

	h.cfg.logger.With(slog.Any("error", err)).Error("unexpected http server failure")

	// Non-blocking publish of the failure (buffer size 1, never overwritten).
	select {
	case h.serveErr <- err:
	default:
	}

	// Ensure the wait group is released and the monitor goroutine unblocked.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), h.cfg.shutdownTimeout)
	defer cancel()

	_ = h.Shutdown(shutdownCtx)
}

// netListener creates a network listener for the given server address and TLS
// configuration. The TCP listener is created through net.ListenConfig so both
// the plain and TLS paths honor the caller's context.
func netListener(ctx context.Context, serverAddr string, tlsConfig *tls.Config) (net.Listener, error) {
	// tls.NewListener performs no configuration validation, so replicate the
	// tls.Listen check upfront (before binding, so nothing can leak) to reject
	// unusable TLS configurations at startup instead of failing every
	// handshake at runtime.
	if tlsConfig != nil && len(tlsConfig.Certificates) == 0 &&
		tlsConfig.GetCertificate == nil && tlsConfig.GetConfigForClient == nil {
		return nil, fmt.Errorf("failed creating the http server address listener: %w", ErrInvalidTLSConfig)
	}

	var lc net.ListenConfig

	ls, err := lc.Listen(ctx, "tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed creating the http server address listener: %w", err)
	}

	if tlsConfig != nil {
		ls = tls.NewListener(ls, tlsConfig)
	}

	return ls, nil
}

// loadRoutes validates and binds the default and custom routes to the router.
// It returns an error (instead of letting the router panic) when a route is
// malformed, duplicated, or rejected by the underlying router.
func loadRoutes(ctx context.Context, binder Binder, cfg *config) error {
	cfg.logger.Debug("loading default routes")

	routes := newDefaultRoutes(cfg)

	cfg.logger.Debug("loading service routes")

	routes = append(routes, binder.BindHTTP(ctx)...)

	cfg.logger.Debug("applying routes")

	// seen tracks method+path pairs (including the index route) to detect duplicates.
	seen := make(map[string]struct{}, len(routes)+1)

	for _, r := range routes {
		err := bindRoute(cfg, r, seen)
		if err != nil {
			return err
		}
	}

	return bindIndexRoute(cfg, routes, seen)
}

// bindRoute validates a single route and registers it on the router.
func bindRoute(cfg *config, r Route, seen map[string]struct{}) error {
	err := validateRoute(r)
	if err != nil {
		return err
	}

	err = reserveRoute(seen, r.Method, r.Path)
	if err != nil {
		return err
	}

	cfg.logger.With(slog.String("path", r.Path)).Debug("binding route")

	// Add default and custom middleware functions.
	middleware := cfg.commonMiddleware(r.DisableLogger, r.Timeout)
	middleware = append(middleware, r.Middleware...)

	handler := ApplyMiddleware(cfg.mwArgs(r.Method, r.Path, r.Description), r.Handler, middleware...)

	return registerRoute(cfg, r.Method, r.Path, handler)
}

// bindIndexRoute registers the generated index route when it is enabled.
func bindIndexRoute(cfg *config, routes []Route, seen map[string]struct{}) error {
	if !cfg.isIndexRouteEnabled() {
		return nil
	}

	cfg.logger.Debug("enabling route index handler")

	err := reserveRoute(seen, http.MethodGet, indexPath)
	if err != nil {
		return err
	}

	indexHandler := cfg.indexHandlerFunc(routes)
	if indexHandler == nil {
		return fmt.Errorf("%w: %s %s (index handler)", ErrNilRouteHandler, http.MethodGet, indexPath)
	}

	disableLogger := cfg.disableDefaultRouteLogger[IndexRoute]
	middleware := cfg.commonMiddleware(disableLogger, 0)

	handler := ApplyMiddleware(cfg.mwArgs(http.MethodGet, indexPath, "Index"), indexHandler, middleware...)

	return registerRoute(cfg, http.MethodGet, indexPath, handler)
}

// validateRoute checks that a route has a valid method and path, a handler,
// and no nil middleware entries.
func validateRoute(r Route) error {
	// httprouter matches methods case-sensitively, so a lowercase method would
	// register a route that standard uppercase requests never reach.
	if r.Method == "" || r.Method != strings.ToUpper(r.Method) {
		return fmt.Errorf("%w: %q %q", ErrInvalidRouteMethod, r.Method, r.Path)
	}

	if r.Path == "" || r.Path[0] != '/' {
		return fmt.Errorf("%w: %q", ErrInvalidRoutePath, r.Path)
	}

	if r.Handler == nil {
		return fmt.Errorf("%w: %s %s", ErrNilRouteHandler, r.Method, r.Path)
	}

	for i, mw := range r.Middleware {
		if mw == nil {
			return fmt.Errorf("%w: %s %s (middleware index %d)", ErrNilRouteMiddleware, r.Method, r.Path, i)
		}
	}

	return nil
}

// reserveRoute records a method+path pair, returning ErrDuplicateRoute if it was already seen.
func reserveRoute(seen map[string]struct{}, method, path string) error {
	key := method + " " + path

	_, dup := seen[key]
	if dup {
		return fmt.Errorf("%w: %s %s", ErrDuplicateRoute, method, path)
	}

	seen[key] = struct{}{}

	return nil
}

// registerRoute binds a handler on the router, converting any router panic
// (e.g. a wildcard conflict or malformed pattern) into an error.
func registerRoute(cfg *config, method, path string, handler http.Handler) (err error) {
	defer func() {
		if rcv := recover(); rcv != nil {
			err = fmt.Errorf("%w: %s %s: %v", ErrRouteRegistration, method, path, rcv)
		}
	}()

	cfg.router.Handler(method, path, handler)

	return nil
}
