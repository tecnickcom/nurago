package httpreverseproxy

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	libhttputil "github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/logutil"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/testutil"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		serviceAddr string
		opts        []Option
		wantErr     bool
	}{
		{
			name:        "fails with invalid character in URL",
			serviceAddr: "http://invalid-url.domain.invalid\u007F",
			wantErr:     true,
		},
		{
			name:        "fails with empty address",
			serviceAddr: "",
			wantErr:     true,
		},
		{
			name:        "fails with missing scheme (host parsed as scheme)",
			serviceAddr: "localhost:8080",
			wantErr:     true,
		},
		{
			name:        "fails with unsupported scheme",
			serviceAddr: "ftp://service.domain.invalid:1234",
			wantErr:     true,
		},
		{
			name:        "fails with missing host",
			serviceAddr: "http://",
			wantErr:     true,
		},
		{
			name:        "succeeds with defaults",
			serviceAddr: "http://service.domain.invalid:1234/",
			wantErr:     false,
		},
		{
			name:        "succeeds with custom http client",
			serviceAddr: "http://service.domain.invalid:1235/",
			opts:        []Option{WithHTTPClient(&testHTTPClient{})},
			wantErr:     false,
		},
		{
			name:        "succeeds with custom reverse proxy",
			serviceAddr: "http://service.domain.invalid:1236/",
			opts:        []Option{WithReverseProxy(&httputil.ReverseProxy{})},
			wantErr:     false,
		},
		{
			name:        "succeeds with custom logger",
			serviceAddr: "http://service.domain.invalid:1237/",
			opts:        []Option{WithLogger(slog.Default())},
			wantErr:     false,
		},
		{
			name:        "succeeds with custom path param",
			serviceAddr: "http://service.domain.invalid:1238/",
			opts:        []Option{WithPathParam("upstream")},
			wantErr:     false,
		},
		{
			name:        "succeeds with empty path param falling back to default",
			serviceAddr: "http://service.domain.invalid:1239/",
			opts:        []Option{WithPathParam("")},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(tt.serviceAddr, tt.opts...)
			if tt.wantErr {
				require.Nil(t, c, "New() returned client should be nil")
				require.Error(t, err, "New() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			require.NotNil(t, c, "New() returned client should not be nil")
			require.NoError(t, err, "New() unexpected error = %v", err)
		})
	}
}

//nolint:gocognit
func TestClient_ForwardRequest(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	// setup target test server
	targetMux := http.NewServeMux()

	targetServer := httptest.NewServer(targetMux)

	t.Cleanup(
		func() {
			targetServer.Close()
		},
	)

	hres := libhttputil.NewHTTPResp(slog.Default())

	targetMux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		defer func(ctx context.Context) {
			hres.SendStatus(ctx, w, http.StatusOK)
		}(r.Context())

		rd, err := httputil.DumpRequest(r, false)
		assert.NoError(t, err)
		t.Logf("%s", string(rd))

		proxyTestURL, err := url.Parse(targetServer.URL)
		assert.NoError(t, err)

		assert.Equal(t, r.Host, proxyTestURL.Host)
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Forwarded-For"))
		assert.Equal(t, "http", r.Header.Get("X-Forwarded-Proto"))
		assert.NotEmpty(t, r.Header.Get("X-Forwarded-Host"), "SetXForwarded must record the inbound host")
	})

	targetMux.HandleFunc("/badrequest", func(w http.ResponseWriter, r *http.Request) {
		hres.SendStatus(r.Context(), w, http.StatusBadRequest)
	})

	// The upstream abruptly drops the connection so the proxy's upstream RoundTrip
	// fails deterministically, exercising the default error handler (502) without a
	// timing race against the client timeout.
	targetMux.HandleFunc("/error", func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}

		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}

		_ = conn.Close()
	})

	tests := []struct {
		name       string
		path       string
		status     int
		withLogger bool
	}{
		{
			name:   "success OK",
			path:   "/proxy/test",
			status: http.StatusOK,
		},
		{
			name:   "Not Found",
			path:   "/proxy/notfound",
			status: http.StatusNotFound,
		},
		{
			name:   "Bad Request",
			path:   "/proxy/badrequest",
			status: http.StatusBadRequest,
		},
		{
			name:   "Backend Error",
			path:   "/proxy/error",
			status: http.StatusBadGateway,
		},
		{
			name:       "Backend Error with logger",
			path:       "/proxy/error",
			status:     http.StatusBadGateway,
			withLogger: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := []Option{}
			if tt.withLogger {
				opts = append(opts, WithLogger(slog.Default()))
			}

			// setup proxy test server
			c, err := New(targetServer.URL, opts...)
			require.NoError(t, err)

			proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)

			proxyServer := httptest.NewServer(proxyMux)

			t.Cleanup(
				func() {
					proxyServer.Close()
				},
			)

			ctx := t.Context()

			// perform test
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxyServer.URL+tt.path, nil)

			hc := &http.Client{Timeout: timeout}
			resp, err := hc.Do(req)

			t.Cleanup(
				func() {
					if resp != nil {
						err := resp.Body.Close()
						require.NoError(t, err)
					}
				},
			)

			require.NoError(t, err)
			require.Equal(t, tt.status, resp.StatusCode)
		})
	}
}

// TestClient_ForwardRequest_PreservesBasePath verifies that the base path of
// the configured upstream address (e.g. "/v2") is preserved by the default
// rewrite instead of being clobbered by the catch-all path parameter.
func TestClient_ForwardRequest_PreservesBasePath(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	targetMux := http.NewServeMux()
	targetServer := httptest.NewServer(targetMux)

	t.Cleanup(func() { targetServer.Close() })

	hres := libhttputil.NewHTTPResp(slog.Default())

	targetMux.HandleFunc("/v2/users", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/users", r.URL.Path)
		hres.SendStatus(r.Context(), w, http.StatusOK)
	})

	// The upstream address carries a base path that must survive the rewrite.
	c, err := New(targetServer.URL + "/v2")
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	ctx := t.Context()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxyServer.URL+"/proxy/users", nil)

	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestClient_ForwardRequest_ForwardsRedirect verifies that the default
// upstream client does not follow 3xx responses: the redirect must be
// forwarded verbatim to the caller instead of being fetched by the proxy.
func TestClient_ForwardRequest_ForwardsRedirect(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	targetMux := http.NewServeMux()
	targetServer := httptest.NewServer(targetMux)

	t.Cleanup(func() { targetServer.Close() })

	targetMux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})

	targetMux.HandleFunc("/redirected", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("FINAL"))
	})

	c, err := New(targetServer.URL)
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	ctx := t.Context()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxyServer.URL+"/proxy/redirect", nil)

	// The test client must not follow redirects either, so it observes the
	// exact status the proxy sent back.
	hc := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusFound, resp.StatusCode, "the proxy must forward the 3xx instead of following it")
	require.Equal(t, "/redirected", resp.Header.Get("Location"))
}

func TestClient_ForwardRequest_CustomPathParam(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	targetMux := http.NewServeMux()
	targetServer := httptest.NewServer(targetMux)

	t.Cleanup(func() { targetServer.Close() })

	hres := libhttputil.NewHTTPResp(slog.Default())

	targetMux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		// The upstream path must be preserved through the custom wildcard param.
		assert.Equal(t, "/test", r.URL.Path)
		hres.SendStatus(r.Context(), w, http.StatusOK)
	})

	// Register the catch-all under a non-default param name and configure it.
	c, err := New(targetServer.URL, WithPathParam("upstream"))
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*upstream", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	ctx := t.Context()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxyServer.URL+"/proxy/test", nil)

	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestClient_ForwardRequest_CustomDirector verifies that a custom reverse proxy
// configured with a legacy Director (and no Rewrite) is left intact: New must not
// inject its own Rewrite, which would conflict with the Director and make
// ReverseProxy reject every request with a 502.
func TestClient_ForwardRequest_CustomDirector(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	var gotHost string

	targetMux := http.NewServeMux()
	targetMux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host

		w.WriteHeader(http.StatusOK)
	})

	targetServer := httptest.NewServer(targetMux)

	t.Cleanup(func() { targetServer.Close() })

	tu, err := url.Parse(targetServer.URL)
	require.NoError(t, err)

	custom := &httputil.ReverseProxy{
		Director: func(req *http.Request) { //nolint:staticcheck // SA1019: intentional deprecated-field test
			req.URL.Scheme = tu.Scheme
			req.URL.Host = tu.Host
			req.URL.Path = "/test"
			req.Host = tu.Host
		},
	}

	c, err := New(targetServer.URL, WithReverseProxy(custom))
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+"/proxy/anything", nil)

	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "custom Director must forward instead of conflicting with an injected Rewrite")
	require.Equal(t, tu.Host, gotHost)
}

// TestNew_PreservesCustomErrorLog verifies that an ErrorLog configured on a
// caller-supplied reverse proxy is not overwritten by New.
func TestNew_PreservesCustomErrorLog(t *testing.T) {
	t.Parallel()

	myLog := logutil.NewLogFromSlog(slog.Default())
	custom := &httputil.ReverseProxy{ErrorLog: myLog}

	c, err := New("http://service.domain.invalid:1234", WithReverseProxy(custom))
	require.NoError(t, err)
	require.Same(t, myLog, c.proxy.ErrorLog)
}

// TestDefaultUpstreamTransport_Tuned verifies the default transport raises the
// per-host idle pool above net/http's default of 2 and bounds only the response
// header wait (no whole-request timeout that would truncate streams).
func TestDefaultUpstreamTransport_Tuned(t *testing.T) {
	t.Parallel()

	tr, ok := defaultUpstreamTransport().(*http.Transport)
	require.True(t, ok)
	require.Equal(t, defaultMaxIdleConnsPerHost, tr.MaxIdleConnsPerHost)
	require.Greater(t, tr.MaxIdleConnsPerHost, 2, "per-host idle pool must exceed net/http's default of 2")
	require.Equal(t, defaultResponseHeaderTimeout, tr.ResponseHeaderTimeout)

	// The tuned transport must be a private clone, never the process-wide default.
	require.NotSame(t, http.DefaultTransport, http.RoundTripper(tr))
}

// stubRoundTripper is an http.RoundTripper that is not an *http.Transport,
// exercising the defaultUpstreamTransport fallback path.
type stubRoundTripper struct{}

func (*stubRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, errors.New("TEST")
}

//nolint:paralleltest // swaps the process-wide http.DefaultTransport.
func TestDefaultUpstreamTransport_NotHTTPTransportFallback(t *testing.T) {
	orig := http.DefaultTransport

	t.Cleanup(func() { http.DefaultTransport = orig })

	rt := &stubRoundTripper{}
	http.DefaultTransport = rt

	require.Same(t, rt, defaultUpstreamTransport())
}

// newTestErrorHandler builds the default error handler capturing its log output.
func newTestErrorHandler(t *testing.T) (errHandler, *bytes.Buffer) {
	t.Helper()

	var buf bytes.Buffer

	c := &Client{
		logger:   slog.New(slog.NewTextHandler(&buf, nil)),
		redactFn: redact.Default().BytesToString,
	}

	return c.newErrorHandler(), &buf
}

// TestDefaultErrorHandler_UpstreamError verifies a genuine upstream failure is
// answered with a 502. The request time is seeded in context to exercise the
// duration-from-context path.
func TestDefaultErrorHandler_UpstreamError(t *testing.T) {
	t.Parallel()

	handler, _ := newTestErrorHandler(t)

	ctx := libhttputil.WithRequestTime(context.Background(), time.Now().Add(-time.Second))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/proxy/x?q=1", nil)
	rec := httptest.NewRecorder()

	handler(rec, req, errors.New("upstream boom"))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), http.StatusText(http.StatusBadGateway))
}

// TestDefaultErrorHandler_ClientGone verifies that both a canceled and a
// deadline-exceeded inbound context are treated as the client going away: logged at
// Info and no 502 written to the abandoned connection.
func TestDefaultErrorHandler_ClientGone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{
			name: "canceled",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				return ctx, cancel
			},
		},
		{
			name: "deadline exceeded",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))

				return ctx, cancel
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler, buf := newTestErrorHandler(t)

			ctx, cancel := tt.ctx()
			defer cancel()

			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/proxy/x", nil)
			rec := httptest.NewRecorder()

			handler(rec, req, errors.New("boom"))

			// The recorder keeps its default 200 because the handler writes nothing.
			require.Equal(t, http.StatusOK, rec.Code)
			require.Empty(t, rec.Body.String())
			require.Contains(t, buf.String(), "proxy_client_closed")
		})
	}
}

// TestDefaultErrorHandler_UpstreamDeadlineIs502 locks in the non-trap behavior: an
// upstream timeout reports as a deadline error, but with a live inbound context it
// must remain a 502 rather than being misread as a client disconnect.
func TestDefaultErrorHandler_UpstreamDeadlineIs502(t *testing.T) {
	t.Parallel()

	handler, buf := newTestErrorHandler(t)

	// Inbound context is alive; the error itself is a deadline (as ResponseHeaderTimeout reports).
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/proxy/x", nil)
	rec := httptest.NewRecorder()

	handler(rec, req, context.DeadlineExceeded)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, buf.String(), "proxy_error")
}

// TestDefaultErrorHandler_RedactsSecrets verifies the request query and the URL
// embedded in the error are redacted in the log entry.
func TestDefaultErrorHandler_RedactsSecrets(t *testing.T) {
	t.Parallel()

	handler, buf := newTestErrorHandler(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/proxy/x?access_token=SUPERSECRET123", nil)
	rec := httptest.NewRecorder()

	err := &url.Error{Op: "Get", URL: "http://upstream/x?access_token=SUPERSECRET123", Err: errors.New("EOF")}
	handler(rec, req, err)

	logline := buf.String()
	require.NotContains(t, logline, "SUPERSECRET123", "query secret must not reach logs")
	require.Contains(t, logline, redact.RedactionMarker)
}

// TestRedactErrorForLog covers the non-url.Error passthrough and the query-less URL
// branch of the error redactor.
func TestRedactErrorForLog(t *testing.T) {
	t.Parallel()

	plain := errors.New("plain")
	require.Same(t, plain, redactErrorForLog(plain, redact.Default().BytesToString))

	// A *url.Error whose URL has no query is returned with the URL unchanged.
	uerr := &url.Error{Op: "Get", URL: "http://upstream/x", Err: errors.New("EOF")}
	got := redactErrorForLog(uerr, redact.Default().BytesToString)
	require.Contains(t, got.Error(), "http://upstream/x")
}

// TestNewErrorHandler_NilRedactFnFallback verifies the handler tolerates a Client
// whose redactFn was never set, falling back to the default redactor.
func TestNewErrorHandler_NilRedactFnFallback(t *testing.T) {
	t.Parallel()

	c := &Client{logger: slog.Default()} // redactFn intentionally nil
	handler := c.newErrorHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/proxy/x?q=1", nil)
	rec := httptest.NewRecorder()

	require.NotPanics(t, func() { handler(rec, req, errors.New("boom")) })
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

// TestClient_ForwardRequest_BasePathBoundary verifies that, by default, a configured
// base path acts as a boundary: requests whose path escapes it are rejected while
// normal paths pass. The proxy answers 400 before contacting the upstream on an
// escape, so the status code alone distinguishes forwarded (200) from rejected (400).
func TestClient_ForwardRequest_BasePathBoundary(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	// A catch-all upstream that would answer 200 for any forwarded path, so a 200
	// proves the request escaped the base path and reached the upstream.
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(func() { targetServer.Close() })

	// Strict enforcement is the default (no option required).
	c, err := New(targetServer.URL + "/v2")
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "within base", path: "/proxy/users", wantStatus: http.StatusOK},
		{name: "dot-dot escape", path: "/proxy/../admin", wantStatus: http.StatusBadRequest},
		{name: "encoded dot-dot escape", path: "/proxy/%2e%2e/admin", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+tt.path, nil)

			hc := &http.Client{
				Timeout:       timeout,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}

			resp, err := hc.Do(req)
			require.NoError(t, err)

			t.Cleanup(func() {
				if resp != nil {
					assert.NoError(t, resp.Body.Close())
				}
			})

			require.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

// TestClient_ForwardRequest_LaxBasePath verifies that WithLaxBasePath disables the
// default boundary check, forwarding a ".." escape to the upstream verbatim (200)
// instead of rejecting it (400).
func TestClient_ForwardRequest_LaxBasePath(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	// A catch-all upstream that answers 200 for any forwarded path, so a 200 proves
	// the escape reached the upstream instead of being rejected.
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(func() { targetServer.Close() })

	c, err := New(targetServer.URL+"/v2", WithLaxBasePath())
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+"/proxy/../admin", nil)

	hc := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "lax mode must forward the escape instead of rejecting it")
}

// TestClient_ForwardRequest_NormalizesBasePath verifies that a non-normalized base
// path in the configured address does not falsely reject legitimate requests: the
// base is cleaned so the strict check and the forwarded path stay consistent.
func TestClient_ForwardRequest_NormalizesBasePath(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	var gotPath string

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path

		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(func() { targetServer.Close() })

	// A base path containing ".." normalizes to "/b"; a legit request must pass.
	c, err := New(targetServer.URL + "/a/../b")
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+"/proxy/users", nil)

	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "a normalized base path must not reject legit requests")
	require.Equal(t, "/b/users", gotPath, "the cleaned base path must be forwarded")
}

// recordingHTTPClient is an HTTPClient that records whether Do was called and
// delegates to http.DefaultClient, exercising the WithHTTPClient forwarding path.
type recordingHTTPClient struct {
	called bool
}

func (r *recordingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	r.called = true

	return http.DefaultClient.Do(req) //nolint:wrapcheck
}

// TestClient_ForwardRequest_CustomHTTPClient verifies a WithHTTPClient client is the
// one that actually performs the upstream request.
func TestClient_ForwardRequest_CustomHTTPClient(t *testing.T) {
	t.Parallel()

	const timeout = 1 * time.Second

	targetMux := http.NewServeMux()
	targetMux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	targetServer := httptest.NewServer(targetMux)

	t.Cleanup(func() { targetServer.Close() })

	rec := &recordingHTTPClient{}

	c, err := New(targetServer.URL, WithHTTPClient(rec))
	require.NoError(t, err)

	proxyMux := testutil.RouterWithHandler(http.MethodGet, "/proxy/*path", c.ForwardRequest)
	proxyServer := httptest.NewServer(proxyMux)

	t.Cleanup(func() { proxyServer.Close() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+"/proxy/ping", nil)

	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, rec.called, "the custom HTTPClient must perform the upstream request")
}

// TestWithFlushInterval verifies the option lands on the ReverseProxy.
func TestWithFlushInterval(t *testing.T) {
	t.Parallel()

	c, err := New("http://service.domain.invalid:1234", WithFlushInterval(250*time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, 250*time.Millisecond, c.proxy.FlushInterval)
}

// TestNew_PreservesReverseProxyFlushInterval verifies that a FlushInterval configured
// on a caller-supplied reverse proxy is not clobbered when WithFlushInterval is unused.
func TestNew_PreservesReverseProxyFlushInterval(t *testing.T) {
	t.Parallel()

	rp := &httputil.ReverseProxy{FlushInterval: 500 * time.Millisecond}

	c, err := New("http://service.domain.invalid:1234", WithReverseProxy(rp))
	require.NoError(t, err)
	require.Equal(t, 500*time.Millisecond, c.proxy.FlushInterval)
}

// TestClient_CloseIdleConnections covers the default (delegating) path, a custom
// transport that does not implement the optional method, and a custom HTTPClient
// that does not implement it.
func TestClient_CloseIdleConnections(t *testing.T) {
	t.Parallel()

	// Default client: reaches the wrapped *http.Client, which closes idle conns.
	def, err := New("http://service.domain.invalid:1234")
	require.NoError(t, err)
	require.NotPanics(t, def.CloseIdleConnections)

	// Custom transport that does not implement CloseIdleConnections: no-op.
	rp := &httputil.ReverseProxy{Transport: &stubRoundTripper{}}
	custom, err := New("http://service.domain.invalid:1234", WithReverseProxy(rp))
	require.NoError(t, err)
	require.NotPanics(t, custom.CloseIdleConnections)

	// Custom HTTPClient without CloseIdleConnections: the wrapper no-ops.
	viaClient, err := New("http://service.domain.invalid:1234", WithHTTPClient(&testHTTPClient{}))
	require.NoError(t, err)
	require.NotPanics(t, viaClient.CloseIdleConnections)
}
