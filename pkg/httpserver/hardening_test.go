package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/traceid"
)

// testBaseURL returns a 127.0.0.1 URL for a server bound to a wildcard
// address, since dialing the wildcard form (e.g. "[::]:port") is not portable.
func testBaseURL(t *testing.T, h *HTTPServer) string {
	t.Helper()

	_, port, err := net.SplitHostPort(h.Addr().String())
	require.NoError(t, err)

	return "http://" + net.JoinHostPort("127.0.0.1", port)
}

// routeBinder is a simple Binder returning a fixed set of routes.
type routeBinder struct {
	routes []Route
}

func (b *routeBinder) BindHTTP(_ context.Context) []Route { return b.routes }

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestNew_nilBinder(t *testing.T) {
	t.Parallel()

	h, err := New(t.Context(), nil, WithServerAddr(":0"))
	require.ErrorIs(t, err, ErrNilBinder)
	require.Nil(t, h)
}

func TestNew_routeValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []Option
		routes  []Route
		wantErr error
	}{
		{
			name:    "empty method",
			routes:  []Route{{Method: "", Path: "/x", Handler: okHandler}},
			wantErr: ErrInvalidRouteMethod,
		},
		{
			name:    "path without leading slash",
			routes:  []Route{{Method: http.MethodGet, Path: "x", Handler: okHandler}},
			wantErr: ErrInvalidRoutePath,
		},
		{
			name:    "empty path",
			routes:  []Route{{Method: http.MethodGet, Path: "", Handler: okHandler}},
			wantErr: ErrInvalidRoutePath,
		},
		{
			name:    "nil handler",
			routes:  []Route{{Method: http.MethodGet, Path: "/x", Handler: nil}},
			wantErr: ErrNilRouteHandler,
		},
		{
			name:    "lowercase method",
			routes:  []Route{{Method: "get", Path: "/x", Handler: okHandler}},
			wantErr: ErrInvalidRouteMethod,
		},
		{
			name: "nil middleware entry",
			routes: []Route{{
				Method:     http.MethodGet,
				Path:       "/x",
				Handler:    okHandler,
				Middleware: []MiddlewareFn{nil},
			}},
			wantErr: ErrNilRouteMiddleware,
		},
		{
			name: "duplicate custom route",
			routes: []Route{
				{Method: http.MethodGet, Path: "/dup", Handler: okHandler},
				{Method: http.MethodGet, Path: "/dup", Handler: okHandler},
			},
			wantErr: ErrDuplicateRoute,
		},
		{
			name:    "malformed wildcard rejected by router",
			routes:  []Route{{Method: http.MethodGet, Path: "/x/*", Handler: okHandler}},
			wantErr: ErrRouteRegistration,
		},
		{
			name: "custom route collides with default route",
			opts: []Option{WithEnableDefaultRoutes(PingRoute)},
			routes: []Route{
				{Method: http.MethodGet, Path: pingHandlerPath, Handler: okHandler},
			},
			wantErr: ErrDuplicateRoute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]Option{WithServerAddr(":0")}, tt.opts...)

			h, err := New(t.Context(), &routeBinder{routes: tt.routes}, opts...)
			require.ErrorIs(t, err, tt.wantErr)
			require.Nil(t, h)
		})
	}
}

func TestNew_indexRouteDuplicate(t *testing.T) {
	t.Parallel()

	binder := &routeBinder{routes: []Route{{Method: http.MethodGet, Path: indexPath, Handler: okHandler}}}

	h, err := New(t.Context(), binder, WithServerAddr(":0"), WithEnableDefaultRoutes(IndexRoute))
	require.ErrorIs(t, err, ErrDuplicateRoute)
	require.Nil(t, h)
}

func TestNew_duplicateDefaultRoutesDeduped(t *testing.T) {
	t.Parallel()

	// Repeated default-route identifiers must be de-duplicated (not rejected).
	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithEnableDefaultRoutes(PingRoute, PingRoute),
	)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NoError(t, h.Shutdown(t.Context()))
}

func TestWithEnableDefaultRoutes_unknown(t *testing.T) {
	t.Parallel()

	h, err := New(t.Context(), NopBinder(), WithServerAddr(":0"), WithEnableDefaultRoutes(DefaultRoute("bogus")))
	require.ErrorIs(t, err, ErrUnknownDefaultRoute)
	require.Nil(t, h)
}

func TestWithTLSConfig(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	custom := &tls.Config{MinVersion: tls.VersionTLS13}
	require.NoError(t, WithTLSConfig(custom)(cfg))
	require.Same(t, custom, cfg.tlsConfig)

	require.Error(t, WithTLSConfig(nil)(cfg))
}

func TestWithoutSelectiveLoggers(t *testing.T) {
	t.Parallel()

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithoutNotFoundLogger(),
		WithoutMethodNotAllowedLogger(),
	)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NoError(t, h.Shutdown(t.Context()))
}

func TestAddr_andServesRequests(t *testing.T) {
	t.Parallel()

	shutdownWG := &sync.WaitGroup{}

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithEnableDefaultRoutes(PingRoute),
		WithShutdownWaitGroup(shutdownWG),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	// Addr must expose the concrete ephemeral port chosen by the OS.
	require.NotNil(t, h.Addr())

	h.StartServer()

	resp, err := http.Get(testBaseURL(t, h) + pingHandlerPath) //nolint:noctx
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, h.Shutdown(t.Context()))
	shutdownWG.Wait()
}

func TestDisableTimeout_route(t *testing.T) {
	t.Parallel()

	// A route flagged with DisableTimeout must outlive a short global timeout.
	binder := &routeBinder{routes: []Route{
		{
			Method:  http.MethodGet,
			Path:    "/slow",
			Timeout: DisableTimeout,
			Handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(50 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			},
		},
	}}

	h, err := New(
		t.Context(),
		binder,
		WithServerAddr(":0"),
		WithRequestTimeout(5*time.Millisecond),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()
	defer func() { _ = h.Shutdown(t.Context()) }()

	resp, err := http.Get(testBaseURL(t, h) + "/slow") //nolint:noctx
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	// Without the exemption the global 5ms timeout would return 503.
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStartServerCtx_doubleStartIgnored(t *testing.T) {
	t.Parallel()

	shutdownWG := &sync.WaitGroup{}

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithShutdownWaitGroup(shutdownWG),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()
	// A second start must be ignored so the wait group stays balanced.
	h.StartServer()

	require.NoError(t, h.Shutdown(t.Context()))

	waitWG(t, shutdownWG)
}

func TestStartServerCtx_afterShutdownIgnored(t *testing.T) {
	t.Parallel()

	shutdownWG := &sync.WaitGroup{}

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithShutdownWaitGroup(shutdownWG),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	// Shutdown before any start: a subsequent start must be a no-op.
	require.NoError(t, h.Shutdown(t.Context()))
	h.StartServer()

	waitWG(t, shutdownWG)
}

type slowConnBinder struct {
	started chan struct{}
	release chan struct{}
}

func (b *slowConnBinder) BindHTTP(_ context.Context) []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/block",
			Handler: func(w http.ResponseWriter, _ *http.Request) {
				close(b.started)
				<-b.release
				w.WriteHeader(http.StatusOK)
			},
		},
	}
}

func TestShutdown_returnsErrorOnActiveConnection(t *testing.T) {
	t.Parallel()

	binder := &slowConnBinder{started: make(chan struct{}), release: make(chan struct{})}

	h, err := New(
		t.Context(),
		binder,
		WithServerAddr(":0"),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()

	blockURL := testBaseURL(t, h) + "/block"

	go func() {
		resp, gerr := http.Get(blockURL) //nolint:noctx,gosec // local test server URL
		if gerr == nil {
			_ = resp.Body.Close()
		}
	}()

	<-binder.started // ensure the request is in-flight

	// An already-expired deadline against an active connection makes the
	// underlying Server.Shutdown return the context error.
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Millisecond)
	defer cancel()

	require.Error(t, h.Shutdown(ctx))

	close(binder.release)
}

func TestPanicHandler_abortHandlerReraised(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.setRouter()

	cfg.router.Handler(http.MethodGet, "/abort", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/abort", nil)
	require.NoError(t, err)

	defer func() {
		// http.ErrAbortHandler must be re-raised so net/http can abort silently.
		rec := recover()
		assert.Equal(t, http.ErrAbortHandler, rec)
	}()

	cfg.router.ServeHTTP(newDiscardWriter(), req)
}

// discardWriter is a minimal http.ResponseWriter used where the response is irrelevant.
type discardWriter struct {
	header http.Header
}

func newDiscardWriter() *discardWriter { return &discardWriter{header: make(http.Header)} }

func (d *discardWriter) Header() http.Header         { return d.header }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(_ int)           {}

// waitWG fails the test if the wait group does not reach zero promptly.
func waitWG(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdownWaitGroup did not reach zero")
	}
}

// drainBinder exposes a route that reports its request-context state after a
// delay, to observe graceful-drain behavior during shutdown.
type drainBinder struct {
	inHandler chan struct{}
	ctxErr    chan error
}

func (b *drainBinder) BindHTTP(_ context.Context) []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/drain",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				close(b.inHandler)
				time.Sleep(150 * time.Millisecond)

				b.ctxErr <- r.Context().Err()

				w.WriteHeader(http.StatusOK)
			},
		},
	}
}

func TestGracefulDrain_appContextCancel(t *testing.T) {
	t.Parallel()

	binder := &drainBinder{inHandler: make(chan struct{}), ctxErr: make(chan error, 1)}
	shutdownWG := &sync.WaitGroup{}

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	h, err := New(
		appCtx,
		binder,
		WithServerAddr(":0"),
		WithShutdownWaitGroup(shutdownWG),
		WithShutdownTimeout(2*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()

	drainURL := testBaseURL(t, h) + "/drain"
	respCode := make(chan int, 1)

	go func() {
		resp, gerr := http.Get(drainURL) //nolint:noctx,gosec // local test server URL
		if gerr != nil {
			respCode <- -1
			return
		}

		_ = resp.Body.Close()
		respCode <- resp.StatusCode
	}()

	<-binder.inHandler // the request is now in-flight

	// Canceling the application context starts a graceful shutdown. The
	// in-flight request must be drained (its context stays alive and the
	// response completes), not canceled with the application context.
	cancelApp()

	require.NoError(t, <-binder.ctxErr, "in-flight request context must survive app-context cancelation")
	require.Equal(t, http.StatusOK, <-respCode, "in-flight request must complete during graceful drain")

	waitWG(t, shutdownWG)
}

func TestWithMiddlewareFn_nil(t *testing.T) {
	t.Parallel()

	h, err := New(t.Context(), NopBinder(), WithServerAddr(":0"), WithMiddlewareFn(nil))
	require.Error(t, err)
	require.Nil(t, h)
}

func TestWithoutDefaultRouteLogger_unknown(t *testing.T) {
	t.Parallel()

	h, err := New(t.Context(), NopBinder(), WithServerAddr(":0"), WithoutDefaultRouteLogger(DefaultRoute("bogus")))
	require.ErrorIs(t, err, ErrUnknownDefaultRoute)
	require.Nil(t, h)
}

func TestNew_nilIndexHandlerResult(t *testing.T) {
	t.Parallel()

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithEnableDefaultRoutes(IndexRoute),
		WithIndexHandlerFunc(func(_ []Route) http.HandlerFunc { return nil }),
	)
	require.ErrorIs(t, err, ErrNilRouteHandler)
	require.Nil(t, h)
}

func TestRequestInjectHandler_nilArgsFallbacks(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Nil logger, redact function, and random generator must fall back to safe
	// defaults instead of panicking.
	handler := RequestInjectHandler(nil, "X-Request-ID", nil, nil, next)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rr.Code)

	// A debug-level logger exercises the request-dump path, which invokes the
	// identity fallback redact function. slog.DiscardHandler cannot be used
	// here: it reports Enabled() as false, which would skip the dump path.
	debugLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})) //nolint:sloglint
	handler = RequestInjectHandler(debugLogger, "X-Request-ID", nil, nil, next)

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHTTP2OverTLS(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := makeTestCert(t)

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithTLSCertData(certPEM, keyPEM),
		WithEnableDefaultRoutes(PingRoute),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()
	defer func() { _ = h.Shutdown(t.Context()) }()

	_, port, err := net.SplitHostPort(h.Addr().String())
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test certificate
			ForceAttemptHTTP2: true,
		},
	}

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"https://"+net.JoinHostPort("127.0.0.1", port)+pingHandlerPath,
		nil,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, resp.ProtoMajor, "expected HTTP/2 via ALPN, got %s", resp.Proto)
}

func TestNew_nilContext(t *testing.T) {
	t.Parallel()

	// A nil context must be treated as context.Background(), not panic.
	h, err := New(nil, NopBinder(), WithServerAddr(":0")) //nolint:staticcheck // deliberately nil
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NoError(t, h.Shutdown(context.Background()))
}

func TestApplyMiddleware_nilEntrySkipped(t *testing.T) {
	t.Parallel()

	applied := false
	mw := func(_ MiddlewareArgs, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applied = true

			next.ServeHTTP(w, r)
		})
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Nil entries must be skipped without panicking; real entries still apply.
	handler := ApplyMiddleware(MiddlewareArgs{}, next, nil, mw, nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, applied, "non-nil middleware must still be applied")
}

func TestRequestInjectHandler_nilRedactFallbackRedacts(t *testing.T) {
	t.Parallel()

	var buf syncLogBuffer

	// Debug level triggers the request-dump path.
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// A nil redact function must fall back to the redacting default, never to
	// an identity function that would leak credentials into the logs.
	handler := RequestInjectHandler(logger, "X-Request-ID", nil, nil, next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer topsecret123")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	require.Contains(t, out, "request_dump")
	require.NotContains(t, out, "topsecret123", "credentials must be redacted from the request dump")
	require.Contains(t, out, "***", "the redaction marker must replace the sensitive value")
}

func TestRequestInjectHandler_responseMetadata(t *testing.T) {
	t.Parallel()

	var buf syncLogBuffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("teapot"))
	})

	handler := RequestInjectHandler(logger, "X-Request-ID", nil, nil, next)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tea", nil))

	out := buf.String()
	require.Contains(t, out, "response_code=418", "the request log must carry the response status")
	require.Contains(t, out, "response_size=6", "the request log must carry the response size")

	// A handler that never calls WriteHeader must be logged with the implicit 200.
	buf.Reset()

	implicit := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	handler = RequestInjectHandler(logger, "X-Request-ID", nil, nil, implicit)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	require.Contains(t, buf.String(), "response_code=200")
}

func TestRequestInjectHandler_traceIDOverridesContext(t *testing.T) {
	t.Parallel()

	var buf syncLogBuffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var got string

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = traceid.FromContext(r.Context(), "")
	})

	handler := RequestInjectHandler(logger, traceid.DefaultHeader, nil, nil, next)

	ctx := traceid.NewContext(t.Context(), "stale-id")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.Header.Set(traceid.DefaultHeader, "request-id")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, "request-id", got, "the request trace ID must replace one already in the context")
	require.Contains(t, buf.String(), "traceid=request-id")
}

func TestNew_baseContextTraceIDDoesNotShadowRequestID(t *testing.T) {
	t.Parallel()

	var buf syncLogBuffer

	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ids := make(chan string, 2)

	binder := &routeBinder{routes: []Route{{
		Method: http.MethodGet,
		Path:   "/id",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			ids <- traceid.FromContext(r.Context(), "")

			w.WriteHeader(http.StatusOK)
		},
	}}}

	// The application context passed to New carries its own trace ID.
	ctx := traceid.NewContext(t.Context(), "app-id")

	h, err := New(ctx, binder, WithServerAddr("127.0.0.1:0"), WithLogger(logger))
	require.NoError(t, err)

	h.StartServer()

	t.Cleanup(func() { _ = h.Shutdown(context.WithoutCancel(ctx)) })

	get := func(header string) {
		req, rerr := http.NewRequestWithContext(t.Context(), http.MethodGet, testBaseURL(t, h)+"/id", nil)
		require.NoError(t, rerr)

		if header != "" {
			req.Header.Set(traceid.DefaultHeader, header)
		}

		resp, rerr := http.DefaultClient.Do(req)
		require.NoError(t, rerr)
		require.NoError(t, resp.Body.Close())
	}

	get("request-id")
	require.Equal(t, "request-id", <-ids)

	get("")

	generated := <-ids
	require.NotEqual(t, "app-id", generated, "a generated trace ID must not be shadowed by the application one")
	require.Len(t, generated, 36)
	require.Contains(t, buf.String(), `"traceid":"`+generated+`"`, "the handler and the log must agree on the trace ID")
}

// syncLogBuffer is a mutex-guarded buffer safe for use as an slog output.
type syncLogBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, p...)

	return len(p), nil
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return string(b.data)
}

func (b *syncLogBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = nil
}

func TestWithTLSConfig_http1WithoutALPN(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := makeTestCert(t)

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	// A custom TLS config that does NOT advertise "h2" via ALPN must serve
	// HTTP/1.1 even to clients attempting HTTP/2: the explicit
	// http.Server.Protocols set does not override ALPN negotiation.
	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithTLSConfig(&tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		}),
		WithEnableDefaultRoutes(PingRoute),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()
	defer func() { _ = h.Shutdown(t.Context()) }()

	_, port, err := net.SplitHostPort(h.Addr().String())
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test certificate
			ForceAttemptHTTP2: true,
		},
	}

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"https://"+net.JoinHostPort("127.0.0.1", port)+pingHandlerPath,
		nil,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 1, resp.ProtoMajor, "without ALPN h2 the connection must fall back to HTTP/1.1, got %s", resp.Proto)
}

// makeTestCert generates an ephemeral self-signed ECDSA certificate for
// 127.0.0.1 and returns the PEM-encoded certificate and key.
func makeTestCert(t *testing.T) ([]byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}
