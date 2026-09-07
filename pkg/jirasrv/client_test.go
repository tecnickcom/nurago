//go:generate mockgen -write_package_comment=false -package jirasrv -destination ./mock_test.go . HTTPClient
package jirasrv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/validator"
	"github.com/undefinedlabs/go-mpatch"
	gomock "go.uber.org/mock/gomock"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		addr        string
		token       string
		opts        []Option
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:    "fails with invalid character in URL",
			addr:    "http://invalid-url.domain.invalid\u007F",
			token:   "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with empty address",
			addr:    "",
			token:   "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with relative address missing scheme and host",
			addr:    "/rest/api/2",
			token:   "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with scheme but missing host",
			addr:    "http://",
			token:   "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with empty api token",
			addr:    "http://service.domain.invalid:1234",
			token:   "",
			wantErr: true,
		},
		{
			name:        "succeeds with defaults",
			addr:        "http://service.domain.invalid:1234",
			token:       "0123456789abcdef",
			wantTimeout: defaultPingTimeout,
			wantErr:     false,
		},
		{
			name:        "succeeds with options",
			addr:        "http://service.domain.invalid:1234",
			token:       "0123456789abcdef",
			opts:        []Option{WithPingTimeout(2 * time.Second)},
			wantTimeout: 2 * time.Second,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.opts = append(tt.opts, WithRetryAttempts(1))

			c, err := New(
				tt.addr,
				tt.token,
				tt.opts...,
			)

			if tt.wantErr {
				require.Nil(t, c, "New() returned client should be nil")
				require.Error(t, err, "New() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			require.NotNil(t, c, "New() returned client should not be nil")
			require.NoError(t, err, "New() unexpected error = %v", err)
			require.Equal(t, tt.wantTimeout, c.pingTimeout, "New() unexpected pingTimeout = %d got %d", tt.wantTimeout, c.pingTimeout)
		})
	}
}

//nolint:paralleltest // mutates the package-level newValidator seam
func TestNew_validatorError(t *testing.T) {
	orig := newValidator

	t.Cleanup(func() { newValidator = orig })

	newValidator = func(...validator.Option) (*validator.Validator, error) {
		return nil, errors.New("test-error")
	}

	c, err := New(
		"http://service.domain.invalid:1234",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.Error(t, err)
	require.Nil(t, c)
}

func TestClient_setRequestHeaders(t *testing.T) {
	t.Parallel()

	c, err := New(
		"https://test.invalid",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://test.invalid", nil)
	require.NoError(t, err)

	c.setRequestHeaders(req)

	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "application/json", req.Header.Get("Accept"))
	require.Equal(t, "Bearer 0123456789abcdef", req.Header.Get("Authorization"))
}

func TestClient_retrierForMethod(t *testing.T) {
	t.Parallel()

	c, err := New(
		"https://test.invalid",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)
	require.NotNil(t, c.readRetrier)
	require.NotNil(t, c.writeRetrier)

	// GET selects the read-oriented retrier; any other method selects the
	// write-oriented one.
	require.Same(t, c.readRetrier, c.retrierForMethod(http.MethodGet))
	require.Same(t, c.writeRetrier, c.retrierForMethod(http.MethodPost))
}

func TestNew_invalidRetryConfig(t *testing.T) {
	t.Parallel()

	// The retriers are built during New, so an invalid retry setting (0 attempts)
	// is rejected at construction instead of failing on every request.
	c, err := New(
		"https://test.invalid",
		"0123456789abcdef",
		WithRetryAttempts(0),
	)

	require.Nil(t, c, "New() returned client should be nil")
	require.Error(t, err, "New() should fail with invalid retry attempts")
}

func Test_httpRequest(t *testing.T) {
	t.Parallel()

	var req io.Reader = strings.NewReader(`{"a":"b"}`)

	timeout := 100 * time.Millisecond

	tests := []struct {
		name       string
		httpMethod string
		urlStr     string
		request    io.Reader
		wantErr    bool
	}{
		{
			name:       "fail invalid URL",
			httpMethod: http.MethodPost,
			urlStr:     "%^*&-ERROR",
			request:    nil,
			wantErr:    true,
		},
		{
			name:       "succeed valid URL and nil body",
			httpMethod: http.MethodPost,
			urlStr:     "https://test.invalid",
			request:    nil,
			wantErr:    false,
		},
		{
			name:       "succeed valid URL with body",
			httpMethod: http.MethodPost,
			urlStr:     "https://test.invalid",
			request:    req,
			wantErr:    false,
		},
		{
			name:       "succeed valid URL with empty body",
			httpMethod: http.MethodPost,
			urlStr:     "https://test.invalid",
			request:    http.NoBody,
			wantErr:    false,
		},
		{
			name:       "succeed valid URL with GET method",
			httpMethod: http.MethodGet,
			urlStr:     "https://test.invalid",
			request:    nil,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(
				"https://test.invalid",
				"0123456789abcdef",
				WithRetryAttempts(1),
				WithTimeout(timeout),
				WithPingTimeout(timeout),
			)
			require.NoError(t, err, "Client.HealthCheck() create client unexpected error = %v", err)

			r, err := c.httpRequest(t.Context(), http.MethodPost, tt.urlStr, tt.request)

			if !tt.wantErr {
				require.NoError(t, err)
				require.NotNil(t, r)
			} else {
				require.Error(t, err)
				require.Nil(t, r)
			}
		})
	}
}

//nolint:gocognit
func TestClient_HealthCheck(t *testing.T) {
	t.Parallel()

	timeout := 100 * time.Millisecond

	tests := []struct {
		name                  string
		pingHandlerDelay      time.Duration
		pingHandlerStatusCode int
		pingAddr              string
		wantErr               bool
	}{
		{
			name:                  "fails because ping url error",
			pingHandlerStatusCode: http.StatusOK,
			pingAddr:              "%^*&-ERROR",
			wantErr:               true,
		},
		{
			name:                  "returns error because of timeout",
			pingHandlerDelay:      timeout + 50*time.Millisecond, // margin absorbs timer jitter under load
			pingHandlerStatusCode: http.StatusOK,
			wantErr:               true,
		},
		{
			name:                  "returns error from endpoint",
			pingHandlerStatusCode: http.StatusInternalServerError,
			wantErr:               true,
		},
		{
			name:                  "returns success from endpoint",
			pingHandlerStatusCode: http.StatusOK,
			wantErr:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hres := httputil.NewHTTPResp(slog.Default())
			mux := http.NewServeMux()

			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if tt.pingHandlerDelay != 0 {
					time.Sleep(tt.pingHandlerDelay)
				}

				hres.SendText(r.Context(), w, tt.pingHandlerStatusCode, `{"test":"OK"}`)
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"0123456789abcdef",
				WithRetryAttempts(1),
				WithTimeout(timeout),
				WithPingTimeout(timeout),
			)
			require.NoError(t, err, "Client.HealthCheck() create client unexpected error = %v", err)

			if tt.pingAddr != "" {
				c.pingAddr = tt.pingAddr
			}

			err = c.HealthCheck(t.Context())
			if tt.wantErr {
				require.Error(t, err, "Client.HealthCheck() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err, "Client.HealthCheck() unexpected error = %v", err)
			}
		})
	}
}

//go:noinline
func newRequestWithContextPatch(_ context.Context, _, _ string, _ io.Reader) (*http.Request, error) {
	return nil, errors.New("ERROR: newRequestWithContextPatch")
}

//nolint:gocognit,paralleltest,gocyclo,cyclop
func TestSendRequest(t *testing.T) {
	hres := httputil.NewHTTPResp(slog.Default())

	type testReqData struct {
		TestField int `mapstructure:"testfield" validate:"required,min=1"`
	}

	tests := []struct {
		name              string
		createMockHandler func(t *testing.T) http.HandlerFunc
		setupMocks        func(client *MockHTTPClient)
		setupPatches      func() (*mpatch.Patch, error)
		req               any
		query             *url.Values
		wantStatusCode    int
		wantErr           bool
	}{
		{
			name: "failed to execute request - transport error",
			setupMocks: func(m *MockHTTPClient) {
				m.EXPECT().Do(gomock.Any()).Return(nil, errors.New("transport error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "failed to execute request - NewRequest error",
			setupPatches: func() (*mpatch.Patch, error) {
				patch, err := mpatch.PatchMethod(http.NewRequestWithContext, newRequestWithContextPatch)
				if err != nil {
					return nil, err //nolint:wrapcheck
				}

				_ = patch.Patch()

				return patch, nil
			},
			wantErr: true,
		},
		{
			name: "unexpected http error status code",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, r *http.Request) {
					hres.SendStatus(r.Context(), w, http.StatusInternalServerError)
				}
			},
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name: "response status < 200",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, r *http.Request) {
					hres.SendText(r.Context(), w, http.StatusSwitchingProtocols, "")
				}
			},
			wantStatusCode: http.StatusSwitchingProtocols,
		},
		{
			name: "response status >= 300",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, r *http.Request) {
					hres.SendText(r.Context(), w, http.StatusMultipleChoices, "")
				}
			},
			wantStatusCode: http.StatusMultipleChoices,
		},
		{
			name:    "invalid request",
			req:     &testReqData{TestField: 0},
			wantErr: true,
		},
		{
			name:    "invalid request type",
			req:     make(chan int), // this payload can't be encoded in JSON
			wantErr: true,
		},
		{
			name: "success valid response",
			req:  &testReqData{TestField: 2},
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, r *http.Request) {
					hres.SendText(r.Context(), w, http.StatusCreated, "Success")
				}
			},
			wantStatusCode: http.StatusCreated,
			wantErr:        false,
		},
	}

	//nolint:paralleltest
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip mpatch-based testcases on Apple Silicon macOS
			if tt.setupPatches != nil && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				t.Skip("mpatch not supported on Mac silicon - skipping test")
			}

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			endpoint := "/test"

			mux := http.NewServeMux()
			if tt.createMockHandler != nil {
				mux.HandleFunc(apiBasePath+endpoint, tt.createMockHandler(t))
			}

			ts := httptest.NewServer(mux)
			defer ts.Close()

			clientOpts := []Option{}

			if tt.setupMocks != nil {
				mc := NewMockHTTPClient(ctrl)
				tt.setupMocks(mc)
				clientOpts = append(clientOpts, WithHTTPClient(mc), WithRetryAttempts(1))
			}

			// remove apiBasePath+endpoint from ts.URL
			tsURL := strings.TrimSuffix(ts.URL, apiBasePath+endpoint)

			c, err := New(
				tsURL,
				"0123456789abcdef",
				clientOpts...,
			)
			require.NoError(t, err)

			if tt.setupPatches != nil {
				patch, err := tt.setupPatches()
				require.NoError(t, err)

				defer func() {
					_ = patch.Unpatch()
				}()
			}

			if tt.req == nil {
				tt.req = &testReqData{
					TestField: 1,
				}
			}

			if tt.query == nil {
				tt.query = &url.Values{}
				tt.query.Set("queryparam", "value")
			}

			resp, err := c.SendRequest(t.Context(), http.MethodPost, endpoint, tt.query, tt.req)

			if tt.wantErr {
				require.Error(t, err, "error expected but got nil")
				return
			}

			require.NotNil(t, resp)
			require.Equal(t, tt.wantStatusCode, resp.StatusCode)

			_ = resp.Body.Close()
		})
	}
}

func TestClient_requestBuffer(t *testing.T) {
	t.Parallel()

	type testReqData struct {
		TestField int `validate:"required,min=1"`
	}

	tests := []struct {
		name    string
		request any
		wantErr bool
	}{
		{
			name:    "success with valid request",
			request: &testReqData{TestField: 5},
			wantErr: false,
		},
		{
			name:    "fails with invalid request - validation error",
			request: &testReqData{TestField: 0},
			wantErr: true,
		},
		{
			name:    "fails with nil request",
			request: nil,
			wantErr: false,
		},
		{
			name:    "fails with non-encodable type",
			request: make(chan int),
			wantErr: true,
		},
		{
			name:    "success with map",
			request: map[string]any{"key": "value"},
			wantErr: false,
		},
		{
			name:    "success with string",
			request: "test string",
			wantErr: false,
		},
		{
			name:    "success with slice",
			request: []int{1, 2, 3},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(
				"https://test.invalid",
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			buf, err := c.requestBuffer(t.Context(), tt.request)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, buf)
			} else {
				require.NoError(t, err)
				require.NotNil(t, buf)
				require.Positive(t, buf.Len())
			}
		})
	}
}

func Test_isValidatable(t *testing.T) {
	t.Parallel()

	type payload struct{}

	require.True(t, isValidatable(payload{}))
	require.True(t, isValidatable(&payload{}))
	require.False(t, isValidatable((*payload)(nil)))
	require.False(t, isValidatable(map[string]string{}))
}

// TestNew_sentinelErrors verifies New wraps its failures in the exported
// sentinels so callers can match them with errors.Is.
func TestNew_sentinelErrors(t *testing.T) {
	t.Parallel()

	_, err := New("http://", "0123456789abcdef", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrInvalidAddress)

	_, err = New("http://service.domain.invalid:1234", "", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrEmptyToken)

	_, err = New("http://service.domain.invalid:1234", "0123456789abcdef", WithRetryAttempts(0))
	require.ErrorIs(t, err, ErrInvalidRetryConfig)
}
