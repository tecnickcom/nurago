package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/ipify"
	"github.com/tecnickcom/nurago/pkg/profiling"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
	"github.com/tecnickcom/nurago/pkg/traceid"
)

// timeoutMessage is the message used for timeout responses.
const timeoutMessage = "TIMEOUT"

// RedactFn redacts sensitive values from logged byte payloads and returns a redacted string.
type RedactFn func(b []byte) string

// IndexHandlerFunc builds an index handler from the registered route list.
type IndexHandlerFunc func([]Route) http.HandlerFunc

// GetPublicIPFunc resolves the service public IP address.
type GetPublicIPFunc func(context.Context) (string, error)

// GetPublicIPDefaultFunc returns the GetPublicIP function for a default ipify client.
func GetPublicIPDefaultFunc() GetPublicIPFunc {
	c, _ := ipify.New() // no errors are returned with default values
	return c.GetPublicIP
}

// config contains the configuration for the HTTP server.
type config struct {
	router                        *httprouter.Router
	serverAddr                    string
	traceIDHeaderName             string
	requestTimeout                time.Duration
	serverReadHeaderTimeout       time.Duration
	serverReadTimeout             time.Duration
	serverWriteTimeout            time.Duration
	serverIdleTimeout             time.Duration
	serverMaxHeaderBytes          int
	maxRequestBodyBytes           int64
	tlsConfig                     *tls.Config
	defaultEnabledRoutes          []DefaultRoute
	indexHandlerFunc              IndexHandlerFunc
	ipHandlerFunc                 http.HandlerFunc
	metricsHandlerFunc            http.HandlerFunc
	pingHandlerFunc               http.HandlerFunc
	pprofHandlerFunc              http.HandlerFunc
	statusHandlerFunc             http.HandlerFunc
	notFoundHandlerFunc           http.HandlerFunc
	methodNotAllowedHandlerFunc   http.HandlerFunc
	panicHandlerFunc              http.HandlerFunc
	redactFn                      RedactFn
	middleware                    []MiddlewareFn
	disableDefaultRouteLogger     map[DefaultRoute]bool
	disableRouteLogger            bool
	disableNotFoundLogger         bool
	disableMethodNotAllowedLogger bool
	logger                        *slog.Logger
	lifecycleOpts                 []srvlifecycle.Option
	httpresp                      *httputil.HTTPResp
	rnd                           *random.Rnd
}

// defaultConfig returns the default configuration for the HTTP server.
func defaultConfig() *config {
	logger := slog.Default()

	cfg := &config{
		router:                    httprouter.New(),
		serverAddr:                ":8017",
		traceIDHeaderName:         traceid.DefaultHeader,
		serverReadHeaderTimeout:   1 * time.Minute,
		serverReadTimeout:         1 * time.Minute,
		serverWriteTimeout:        1 * time.Minute,
		serverIdleTimeout:         1 * time.Minute,
		defaultEnabledRoutes:      nil,
		redactFn:                  redact.Default().BytesToString,
		middleware:                []MiddlewareFn{},
		disableDefaultRouteLogger: make(map[DefaultRoute]bool, len(allDefaultRoutes())),
		logger:                    logger,
		httpresp:                  httputil.NewHTTPResp(logger),
		rnd:                       random.New(nil),
	}

	cfg.pprofHandlerFunc = profiling.PProfHandler
	cfg.indexHandlerFunc = cfg.defaultIndexHandler
	cfg.ipHandlerFunc = cfg.defaultIPHandler(GetPublicIPDefaultFunc())
	cfg.metricsHandlerFunc = cfg.notImplementedHandler()
	cfg.pingHandlerFunc = cfg.defaultPingHandler()
	cfg.statusHandlerFunc = cfg.defaultStatusHandler()
	cfg.notFoundHandlerFunc = cfg.defaultNotFoundHandlerFunc()
	cfg.methodNotAllowedHandlerFunc = cfg.defaultMethodNotAllowedHandlerFunc()
	cfg.panicHandlerFunc = cfg.defaultPanicHandlerFunc()

	return cfg
}

// isIndexRouteEnabled checks if the index route is enabled in the configuration.
func (c *config) isIndexRouteEnabled() bool {
	return slices.Contains(c.defaultEnabledRoutes, IndexRoute)
}

// lifecycleOptions returns the srvlifecycle options of the server: the HTTP
// defaults followed by the shutdown options.
func (c *config) lifecycleOptions() []srvlifecycle.Option {
	return append([]srvlifecycle.Option{
		srvlifecycle.WithName("http"),
		srvlifecycle.WithLogger(c.logger),
	}, c.lifecycleOpts...)
}

// commonMiddleware returns the common middleware for all routes.
func (c *config) commonMiddleware(noRouteLogger bool, rTimeout time.Duration) []MiddlewareFn {
	middleware := []MiddlewareFn{}

	// The body-size guard is outermost so it wraps the original response
	// writer (enabling net/http's request-too-large connection handling) and
	// caps even the debug request dump performed by the logger middleware.
	if c.maxRequestBodyBytes > 0 {
		maxBodyMiddlewareFn := func(_ MiddlewareArgs, next http.Handler) http.Handler {
			return http.MaxBytesHandler(next, c.maxRequestBodyBytes)
		}

		middleware = append(middleware, maxBodyMiddlewareFn)
	}

	if !c.disableRouteLogger && !noRouteLogger {
		middleware = append(middleware, LoggerMiddlewareFn)
	}

	// A positive per-route timeout overrides the global one; a negative value
	// (e.g. DisableTimeout) disables the timeout for the route entirely.
	timeout := c.requestTimeout
	if rTimeout != 0 {
		timeout = rTimeout
	}

	if timeout > 0 {
		timeoutMiddlewareFn := func(_ MiddlewareArgs, next http.Handler) http.Handler {
			return http.TimeoutHandler(next, timeout, timeoutMessage)
		}

		middleware = append(middleware, timeoutMiddlewareFn)
	}

	return append(middleware, c.middleware...)
}

// mwArgs builds the MiddlewareArgs shared by every route and default handler.
func (c *config) mwArgs(method, path, description string) MiddlewareArgs {
	return MiddlewareArgs{
		Method:            method,
		Path:              path,
		Description:       description,
		TraceIDHeaderName: c.traceIDHeaderName,
		RedactFunc:        c.redactFn,
		Logger:            c.logger,
		Rnd:               c.rnd,
	}
}

// setRouter sets the router's default handlers if they are not already set.
func (c *config) setRouter() {
	if c.router.NotFound == nil {
		c.router.NotFound = ApplyMiddleware(
			c.mwArgs("", "404", http.StatusText(http.StatusNotFound)),
			c.notFoundHandlerFunc,
			c.commonMiddleware(c.disableNotFoundLogger, 0)...,
		)
	}

	if c.router.MethodNotAllowed == nil {
		c.router.MethodNotAllowed = ApplyMiddleware(
			c.mwArgs("", "405", http.StatusText(http.StatusMethodNotAllowed)),
			c.methodNotAllowedHandlerFunc,
			c.commonMiddleware(c.disableMethodNotAllowedLogger, 0)...,
		)
	}

	if c.router.PanicHandler == nil {
		c.router.PanicHandler = c.newPanicHandler(c.commonMiddleware(false, 0))
	}
}

// newPanicHandler builds the router panic handler. Panics carrying
// http.ErrAbortHandler are re-raised so net/http can abort the connection
// silently (its documented contract); all other panics are logged with a stack
// trace and answered through the configured panic handler.
func (c *config) newPanicHandler(middleware []MiddlewareFn) func(http.ResponseWriter, *http.Request, any) {
	handler := ApplyMiddleware(
		c.mwArgs("", "500", http.StatusText(http.StatusInternalServerError)),
		c.panicHandlerFunc,
		middleware...,
	)

	return func(w http.ResponseWriter, r *http.Request, p any) {
		perr, ok := p.(error)
		if ok && errors.Is(perr, http.ErrAbortHandler) {
			panic(p)
		}

		c.logger.With(
			slog.Any("error", p),
			slog.String("stacktrace", string(debug.Stack())),
		).Error("panic")

		handler.ServeHTTP(w, r)
	}
}

// defaultIndexHandler returns the default index handler.
// The rendered list contains every bound route except the index route itself.
func (c *config) defaultIndexHandler(routes []Route) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			data := &Index{Routes: routes}
			c.httpresp.SendJSON(r.Context(), w, http.StatusOK, data)
		},
	)
}

// defaultIPHandler returns the default /ip handler.
func (c *config) defaultIPHandler(fn GetPublicIPFunc) http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			status := http.StatusOK

			ip, err := fn(r.Context())
			if err != nil {
				status = http.StatusFailedDependency
			}

			c.httpresp.SendText(r.Context(), w, status, ip)
		},
	)
}

// defaultPingHandler returns the default /ping handler.
func (c *config) defaultPingHandler() http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			c.httpresp.SendStatus(r.Context(), w, http.StatusOK)
		},
	)
}

// defaultStatusHandler returns the default /status handler.
func (c *config) defaultStatusHandler() http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			c.httpresp.SendStatus(r.Context(), w, http.StatusOK)
		},
	)
}

// notImplementedHandler returns a 501 Not Implemented response.
func (c *config) notImplementedHandler() http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			c.httpresp.SendStatus(r.Context(), w, http.StatusNotImplemented)
		},
	)
}

// defaultNotFoundHandlerFunc returns the default 404 Not Found handler function.
func (c *config) defaultNotFoundHandlerFunc() http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			c.httpresp.SendStatus(r.Context(), w, http.StatusNotFound)
		},
	)
}

// defaultMethodNotAllowedHandlerFunc returns the default 405 Method Not Allowed handler function.
func (c *config) defaultMethodNotAllowedHandlerFunc() http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			c.httpresp.SendStatus(r.Context(), w, http.StatusMethodNotAllowed)
		},
	)
}

// defaultPanicHandlerFunc returns the default panic handler function.
func (c *config) defaultPanicHandlerFunc() http.HandlerFunc {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			c.httpresp.SendStatus(r.Context(), w, http.StatusInternalServerError)
		},
	)
}
