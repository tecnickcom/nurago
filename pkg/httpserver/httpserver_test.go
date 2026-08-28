//go:generate go tool mockgen -write_package_comment=false -package httpserver -destination ./mock_test.go . Binder

package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"go.uber.org/mock/gomock"
)

func TestNopBinder(t *testing.T) {
	t.Parallel()
	require.NotNil(t, NopBinder())
}

func Test_nopBinder_BindHTTP(t *testing.T) {
	t.Parallel()
	require.Nil(t, NopBinder().BindHTTP(t.Context()))
}

type customMiddlewareBinder struct {
	firstMiddleware  chan struct{}
	secondMiddleware chan struct{}
}

func (c *customMiddlewareBinder) BindHTTP(_ context.Context) []Route {
	return []Route{
		{
			Method:      http.MethodGet,
			Path:        "/hello",
			Description: "Test endpoint",
			Handler:     c.handler,
			Middleware:  []MiddlewareFn{c.middleware(c.firstMiddleware), c.middleware(c.secondMiddleware)},
			Timeout:     10 * time.Second,
		},
		{
			Method:      http.MethodGet,
			Path:        "/timeout",
			Description: "Timeout endpoint",
			Handler:     c.slowHandler,
			Middleware:  []MiddlewareFn{c.middleware(c.firstMiddleware), c.middleware(c.secondMiddleware)},
			Timeout:     1 * time.Millisecond,
		},
	}
}

func (c *customMiddlewareBinder) handler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (c *customMiddlewareBinder) slowHandler(w http.ResponseWriter, _ *http.Request) {
	time.Sleep(2 * time.Millisecond)
	w.WriteHeader(http.StatusOK)
}

func (c *customMiddlewareBinder) middleware(ch chan struct{}) MiddlewareFn {
	return func(_ MiddlewareArgs, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ch <- struct{}{}

			next.ServeHTTP(w, r)
		})
	}
}

func Test_customMiddlewares(t *testing.T) {
	t.Parallel()

	binder := &customMiddlewareBinder{
		firstMiddleware:  make(chan struct{}),
		secondMiddleware: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cfg := defaultConfig()
	cfg.setRouter()
	require.NoError(t, loadRoutes(ctx, binder, cfg))

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-binder.firstMiddleware:
		}

		select {
		case <-ctx.Done():
			return
		case <-binder.secondMiddleware:
		}
	}()

	resp := httptest.NewRecorder()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:1234/hello", nil)
	require.NoError(t, err, "failed to create request")
	cfg.router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code, "unexpected response code")
	require.NoError(t, ctx.Err(), "context should not be canceled")

	resp = httptest.NewRecorder()
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:1234/timeout", nil)
	require.NoError(t, err, "failed to create request")
	cfg.router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusServiceUnavailable, resp.Code, "unexpected response code")
	require.NoError(t, ctx.Err(), "context should not be canceled")
}

//nolint:gocognit
func TestStartServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		opts        []Option
		failListen  bool
		setupBinder func(*MockBinder)
		shutdownSig bool
		wantErr     bool
	}{
		{
			name: "fail with invalid config",
			opts: []Option{
				WithTraceIDHeaderName(""),
			},
			wantErr: true,
		},
		{
			name: "fail with option error",
			opts: []Option{
				WithTLSCertData([]byte(``), []byte(``)),
			},
			wantErr: true,
		},
		{
			name: "fail listen port already bound",
			opts: []Option{
				WithShutdownTimeout(1 * time.Millisecond),
			},
			setupBinder: func(b *MockBinder) {
				b.EXPECT().BindHTTP(gomock.Any()).Times(1)
			},
			failListen: true,
			wantErr:    true,
		},
		{
			name: "succeed",
			opts: []Option{
				WithServerAddr(":0"),
				WithRequestTimeout(1 * time.Minute),
				WithShutdownTimeout(1 * time.Millisecond),
				WithEnableAllDefaultRoutes(),
				WithMiddlewareFn(func(_ MiddlewareArgs, next http.Handler) http.Handler { return next }),
				WithShutdownTimeout(1 * time.Second),
			},
			setupBinder: func(b *MockBinder) {
				b.EXPECT().BindHTTP(gomock.Any()).Times(1)
			},
			wantErr: false,
		},
		{
			name: "succeed and shutdown with signal",
			opts: []Option{
				WithServerAddr(":0"),
				WithShutdownTimeout(1 * time.Second),
			},
			setupBinder: func(b *MockBinder) {
				b.EXPECT().BindHTTP(gomock.Any()).Times(1)
			},
			shutdownSig: true,
			wantErr:     false,
		},
		{
			name: "succeed w/ TLS",
			opts: []Option{
				WithTLSCertData([]byte(`-----BEGIN CERTIFICATE-----
MIICBjCCAW8CFB9PJprToZgFfDJpt3Qk6JIEaMEEMA0GCSqGSIb3DQEBCwUAMEIx
CzAJBgNVBAYTAlhYMRUwEwYDVQQHDAxEZWZhdWx0IENpdHkxHDAaBgNVBAoME0Rl
ZmF1bHQgQ29tcGFueSBMdGQwHhcNMjAwNzIyMTMyMTExWhcNMzAwNzIwMTMyMTEx
WjBCMQswCQYDVQQGEwJYWDEVMBMGA1UEBwwMRGVmYXVsdCBDaXR5MRwwGgYDVQQK
DBNEZWZhdWx0IENvbXBhbnkgTHRkMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKB
gQDTHo34VDfPXuDR4mDPpfh8hvja8loIB60b/qvv81TnJEyjLRzaI4dXclFZwUWC
zWi6LxgVcpILMG4n2KieK4h22EsaQZ7ncZ6pLTHlNJfQXWcHzUmwbA1CNyxJN72Q
LLLE3yw8Xm5AM4QegPJQ3+I27GTnAocygqVKX+aU8rUdgQIDAQABMA0GCSqGSIb3
DQEBCwUAA4GBAE3CSgcBH2P2Y0vvjyijavSCIyvau3ex1cmmybZBDen9aGhw34X5
iotTHm8vUEMtinenht11ypQhxefAreTg0RjsZuCzHlgOQrUIpY5qNSTBNTChbU/b
V6QQpxzrYshYcFuiGxSAdZMa8AFVB4Wan7Ji+vvDTJOyXbDqxA3kLFLi
-----END CERTIFICATE-----`), []byte(`-----BEGIN PRIVATE KEY-----
MIICdQIBADANBgkqhkiG9w0BAQEFAASCAl8wggJbAgEAAoGBANMejfhUN89e4NHi
YM+l+HyG+NryWggHrRv+q+/zVOckTKMtHNojh1dyUVnBRYLNaLovGBVykgswbifY
qJ4riHbYSxpBnudxnqktMeU0l9BdZwfNSbBsDUI3LEk3vZAsssTfLDxebkAzhB6A
8lDf4jbsZOcChzKCpUpf5pTytR2BAgMBAAECgYBPSNZAQECFXDhKGh4JXWcoPPgQ
IZu2EEvui4G+pz9nXrZ5QWPoeBdHu+LZNkAIk2OVKEJ/K3u1QAbeZ/tLC0Y/zGmS
Nv0wgCQ+A4FfQH6l5Hh3jrxFDgbjv+Lrb3Np52AC/NIU0DamNK0VffM/kZpj6Gl0
6uUtqwZwh57rJXjMkQJBAPub3EyG1p3/2CEMm2B7jmn5S+qXKgNdA681mvHY2Q6u
hhtIVtKgEV/yTvx4U6JqD1EAm8MpjfqcGHKqXIqJLn8CQQDWzct+hh5AXrirSz7o
j4WxtWuYRDr+2BWFRee0s5CaWy0y7L3fOv+RwbfFSmBgsGPSq+zXKcvOGU0S5Oca
87P/AkAxinbN+p63bXC40SqmzK014Ig6IJl9IAthrERd6jySz3pIVO4DetDw+1zi
CS8ug4OQh3Yj70KtXZ7StQiTnn8xAkBgE4I+YDytq/BLZYeIu5Ef8DZkz7fXfsz5
ZFAD6gD2mWt5CJzQePIQvqW0z9SVyq+Lbiyr/FzVHUn09n9L9c7/AkA1VDTPiY/H
DSk+QcX0L58Fc7RiaBnykcJRfHnd15MlyqtUJ02iitNJOoSVBNQzr59Iyt7nGBzm
YlAqGKDZ+A+l
-----END PRIVATE KEY-----`)),
				WithServerAddr(":0"),
				WithShutdownTimeout(1 * time.Millisecond),
				WithEnableAllDefaultRoutes(),
			},
			setupBinder: func(b *MockBinder) {
				b.EXPECT().BindHTTP(gomock.Any()).Times(1)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockBinder := NewMockBinder(mockCtrl)
			if tt.setupBinder != nil {
				tt.setupBinder(mockBinder)
			}

			opts := tt.opts

			shutdownWG := &sync.WaitGroup{}
			shutdownSG := make(chan struct{})

			opts = append(opts, WithShutdownWaitGroup(shutdownWG))
			opts = append(opts, WithShutdownSignalChan(shutdownSG))

			ctx, cancelCtx := context.WithCancel(t.Context())

			defer func() {
				if tt.shutdownSig {
					close(shutdownSG)
				}

				time.Sleep(100 * time.Millisecond)
				cancelCtx()
			}()

			if tt.failListen {
				var lc net.ListenConfig

				l, err := lc.Listen(t.Context(), "tcp", ":0")
				require.NoError(t, err, "failed starting pre-listener")

				defer func() { _ = l.Close() }()

				// Binding the already-taken ephemeral address must fail.
				opts = append(opts, WithServerAddr(l.Addr().String()))
			}

			h, err := New(ctx, mockBinder, opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewLogger() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil || h == nil {
				return
			}

			h.StartServer()
		})
	}
}

type mockListenerErr struct{}

func (ls mockListenerErr) Accept() (net.Conn, error) {
	return nil, errors.New("ERROR")
}

func (ls mockListenerErr) Close() error {
	return errors.New("ERROR")
}

func (ls mockListenerErr) Addr() net.Addr {
	return nil
}

func Test_Serve_error(t *testing.T) {
	t.Parallel()

	h := &HTTPServer{
		cfg: defaultConfig(),
		ctx: t.Context(),
		httpServer: &http.Server{
			Addr:              ":54321",
			ReadHeaderTimeout: 1 * time.Millisecond,
			ReadTimeout:       1 * time.Millisecond,
			WriteTimeout:      1 * time.Millisecond,
		},
		listener:     mockListenerErr{},
		shutdownDone: make(chan struct{}),
		monitorDone:  make(chan struct{}),
		serveErr:     make(chan error, 1),
	}

	h.serve()

	// An abnormal Serve failure is surfaced on the ServeError channel and must
	// trigger shutdown so the wait group is released.
	select {
	case err := <-h.ServeError():
		require.Error(t, err, "expected the serve failure to be published")
	default:
		t.Fatal("serve failure was not published on ServeError")
	}
}

func Test_Serve_error_channelFull(t *testing.T) {
	t.Parallel()

	h := &HTTPServer{
		cfg: defaultConfig(),
		ctx: t.Context(),
		httpServer: &http.Server{
			Addr:              ":54321",
			ReadHeaderTimeout: 1 * time.Millisecond,
			ReadTimeout:       1 * time.Millisecond,
			WriteTimeout:      1 * time.Millisecond,
		},
		listener:     mockListenerErr{},
		shutdownDone: make(chan struct{}),
		monitorDone:  make(chan struct{}),
		serveErr:     make(chan error, 1),
	}

	// Pre-fill the buffer so the non-blocking publish drops the new failure
	// instead of blocking or overwriting the first one.
	firstErr := errors.New("first failure")
	h.serveErr <- firstErr

	h.serve()

	select {
	case err := <-h.ServeError():
		require.ErrorIs(t, err, firstErr, "the first published failure must be preserved")
	default:
		t.Fatal("expected the first serve failure to remain published")
	}

	select {
	case err := <-h.ServeError():
		t.Fatalf("the second serve failure must be dropped, got: %v", err)
	default:
	}
}

func TestNew_setsIdleTimeout(t *testing.T) {
	t.Parallel()

	idle := 42 * time.Second
	shutdownWG := &sync.WaitGroup{}

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithServerIdleTimeout(idle),
		WithShutdownWaitGroup(shutdownWG),
	)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Equal(t, idle, h.httpServer.IdleTimeout)

	// Shutdown on a never-started server must not panic and must not decrement
	// the wait group (its counter stays balanced at zero).
	require.NoError(t, h.Shutdown(t.Context()))
	shutdownWG.Wait()
}

func TestShutdown_idempotent(t *testing.T) {
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
	require.NotNil(t, h)

	// Register one running server with the wait group.
	h.StartServer()

	// Calling Shutdown multiple times must not panic and must decrement the
	// wait group exactly once (otherwise Wait would unblock prematurely / the
	// counter would go negative and panic).
	require.NoError(t, h.Shutdown(t.Context()))
	require.NoError(t, h.Shutdown(t.Context()))
	require.NoError(t, h.Shutdown(t.Context()))

	// The internal done channel must be closed by the first Shutdown so the
	// shutdown-monitor goroutine started by StartServer does not leak.
	select {
	case <-h.shutdownDone:
	default:
		t.Fatal("shutdownDone channel not closed: the shutdown-monitor goroutine leaks")
	}

	// Wait for the monitor goroutine to observe shutdownDone and exit. This is
	// done before the test returns (canceling t.Context) so the goroutine
	// deterministically wakes on the shutdownDone case, not on ctx.Done.
	select {
	case <-h.monitorDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown-monitor goroutine did not exit after direct Shutdown")
	}

	// The single internal decrement must balance the StartServer Add(1).
	done := make(chan struct{})

	go func() {
		shutdownWG.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdownWaitGroup did not reach zero: decremented the wrong number of times")
	}
}

func TestShutdown_neverStarted(t *testing.T) {
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
	require.NotNil(t, h)

	// Capture the concrete bound address before releasing it.
	addr := h.Addr().String()

	// Shutdown on a never-started server must not panic (no negative wait group
	// counter) and must release the bound listener so the address can be reused.
	require.NoError(t, h.Shutdown(t.Context()))
	require.NoError(t, h.Shutdown(t.Context()))

	shutdownWG.Wait()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", addr)
	require.NoError(t, err, "the listener was not released by Shutdown")

	require.NoError(t, l.Close())
}

func TestStartServerCtx_contextAlreadyCanceled(t *testing.T) {
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
	require.NotNil(t, h)

	// An already-canceled context must trigger the internal shutdown goroutine,
	// which calls Shutdown once. The wait group must end up balanced even if the
	// caller also invokes Shutdown manually (double path, single decrement).
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	h.StartServerCtx(ctx)

	// Manual shutdown racing with the internal goroutine must be safe.
	require.NoError(t, h.Shutdown(t.Context()))

	done := make(chan struct{})

	go func() {
		shutdownWG.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownWaitGroup did not reach zero")
	}
}

func TestRequestInjectHandler_concurrentLoggerIsolation(t *testing.T) {
	t.Parallel()

	const goroutines = 50

	// The next handler asserts that the trace id propagated through the request
	// context matches the per-request trace id header, proving the per-request
	// logger/context is not shared/mutated across concurrent requests.
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		want := r.Header.Get(traceid.DefaultHeader)
		got := traceid.FromContext(r.Context(), "")
		assert.Equal(t, want, got, "trace id leaked across concurrent requests")
	})

	rnd := random.New(nil)
	logger := slog.New(slog.DiscardHandler)
	handler := RequestInjectHandler(logger, traceid.DefaultHeader, redact.Default().BytesToString, rnd, nextHandler)

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for i := range goroutines {
		go func() {
			defer wg.Done()

			id := fmt.Sprintf("00000000-0000-7000-8000-%012d", i)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			req.Header.Set(traceid.DefaultHeader, id)

			handler.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}

	wg.Wait()
}

func TestNew_setsMaxHeaderBytes(t *testing.T) {
	t.Parallel()

	maxHeaderBytes := 1 << 16

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithServerMaxHeaderBytes(maxHeaderBytes),
	)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Equal(t, maxHeaderBytes, h.httpServer.MaxHeaderBytes)
	require.NoError(t, h.Shutdown(t.Context()))
}

// bodyReaderBinder binds a route that reads the whole request body and
// answers 413 when the read fails with *http.MaxBytesError.
type bodyReaderBinder struct{}

func (b *bodyReaderBinder) BindHTTP(_ context.Context) []Route {
	return []Route{
		{
			Method:      http.MethodPost,
			Path:        "/echo-size",
			Description: "Reads the request body.",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
						w.WriteHeader(http.StatusRequestEntityTooLarge)
						return
					}

					w.WriteHeader(http.StatusInternalServerError)

					return
				}

				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintf(w, "%d", len(data))
			},
		},
	}
}

func TestWithMaxRequestBodyBytes_enforced(t *testing.T) {
	t.Parallel()

	h, err := New(
		t.Context(),
		&bodyReaderBinder{},
		WithServerAddr(":0"),
		WithMaxRequestBodyBytes(1<<10), // 1 KiB
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	h.StartServer()
	defer func() { _ = h.Shutdown(t.Context()) }()

	_, port, err := net.SplitHostPort(h.Addr().String())
	require.NoError(t, err)

	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/echo-size"

	// A body within the limit must pass through untouched.
	small := strings.NewReader(strings.Repeat("a", 1<<8))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, small)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A body over the limit must fail the handler's read with
	// *http.MaxBytesError, yielding 413.
	big := strings.NewReader(strings.Repeat("a", 1<<12))
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, url, big)
	require.NoError(t, err)

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestNew_invalidTLSConfig(t *testing.T) {
	t.Parallel()

	// A TLS configuration without certificate material must be rejected at
	// startup (matching tls.Listen semantics), not accepted only to fail every
	// handshake at runtime.
	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}),
	)
	require.ErrorIs(t, err, ErrInvalidTLSConfig)
	require.Nil(t, h)
}

func TestNew_tlsConfigWithGetCertificate(t *testing.T) {
	t.Parallel()

	// Certificate material supplied via GetCertificate (instead of the static
	// Certificates list) must pass the startup validation.
	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithTLSConfig(&tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return nil, nil //nolint:nilnil // never invoked in this test
			},
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NoError(t, h.Shutdown(t.Context()))
}

func TestShutdown_neverStartedListenerCloseError(t *testing.T) {
	t.Parallel()

	h, err := New(
		t.Context(),
		NopBinder(),
		WithServerAddr(":0"),
		WithShutdownTimeout(1*time.Second),
	)
	require.NoError(t, err)

	// Close the listener beforehand so Shutdown's explicit close of the
	// never-started server's listener fails, covering the error-logging branch.
	require.NoError(t, h.listener.Close())
	require.NoError(t, h.Shutdown(t.Context()))
}
