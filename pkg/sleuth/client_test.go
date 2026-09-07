//go:generate mockgen -write_package_comment=false -package sleuth -destination ./mock_test.go . HTTPClient
package sleuth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/validator"
	"github.com/undefinedlabs/go-mpatch"
	"go.uber.org/mock/gomock"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		addr        string
		org         string
		apikey      string
		opts        []Option
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:    "fails with invalid character in URL",
			addr:    "http://invalid-url.domain.invalid\u007F",
			org:     "testorg",
			apikey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with empty org",
			addr:    "http://service.domain.invalid:1234",
			org:     "",
			apikey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with empty api key",
			addr:    "http://service.domain.invalid:1234",
			org:     "testorg",
			apikey:  "",
			wantErr: true,
		},
		{
			name:        "succeeds with defaults",
			addr:        "http://service.domain.invalid:1234",
			org:         "testorg",
			apikey:      "0123456789abcdef",
			wantTimeout: defaultPingTimeout,
			wantErr:     false,
		},
		{
			name:        "succeeds with options",
			addr:        "http://service.domain.invalid:1234",
			org:         "testorg",
			apikey:      "0123456789abcdef",
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
				tt.org,
				tt.apikey,
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

func TestNew_invalidRetryConfig(t *testing.T) {
	t.Parallel()

	// The retrier is built during New, so an invalid retry setting (0 attempts)
	// is rejected at construction instead of failing on every request.
	c, err := New(
		"http://service.domain.invalid:1234",
		"testorg",
		"0123456789abcdef",
		WithRetryAttempts(0),
	)

	require.Nil(t, c, "New() returned client should be nil")
	require.Error(t, err, "New() should fail with invalid retry attempts")
}

func TestNew_invalidAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
	}{
		{
			name: "relative address fails to parse",
			addr: "invalid-relative-address",
		},
		{
			name: "missing scheme",
			addr: "//host/path",
		},
		{
			name: "missing host",
			addr: "http:///path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(
				tt.addr,
				"testorg",
				"0123456789abcdef",
				WithRetryAttempts(1),
			)

			require.Error(t, err, "New() expected error for invalid address")
			require.Nil(t, c, "New() returned client should be nil")
		})
	}
}

// TestNew_trailingSlashAddr verifies that a trailing slash in addr does not
// produce "//" in the endpoint URLs (they are built with URL.JoinPath).
func TestNew_trailingSlashAddr(t *testing.T) {
	t.Parallel()

	c, err := New(
		"https://app.sleuth.invalid/api/1/",
		"testorg",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	base := "https://app.sleuth.invalid/api/1"

	require.Equal(t, base+"/deployments/testorg/-/register_deploy", c.pingURL)
	require.Equal(t, base+"/deployments/testorg/%s/register_deploy", c.deployRegistrationURLFormat)
	require.Equal(t, base+"/deployments/testorg/%s/register_manual_deploy", c.manualChangeURLFormat)
	require.Equal(t, base+"/deployments/testorg/%s/%s/%s/register_impact/%s", c.customIncidentURLFormat)
	require.Equal(t, base+"/impact/%d/register_impact", c.customMetricURLFormat)
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
		"testorg",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)

	require.Error(t, err, "New() expected error when validator init fails")
	require.Nil(t, c, "New() returned client should be nil")
}

//nolint:gocognit
func TestClient_HealthCheck(t *testing.T) {
	t.Parallel()

	timeout := 100 * time.Millisecond

	tests := []struct {
		name                  string
		pingHandlerDelay      time.Duration
		pingHandlerStatusCode int
		pingURL               string
		pingBody              string
		wantErr               bool
	}{
		{
			name:                  "fails because ping url error",
			pingHandlerStatusCode: http.StatusOK,
			pingURL:               "%^*&-ERROR",
			pingBody:              "Deployment - Not Found",
			wantErr:               true,
		},
		{
			name:                  "returns error because of timeout",
			pingHandlerDelay:      timeout + 50*time.Millisecond, // margin absorbs timer jitter under load
			pingHandlerStatusCode: http.StatusNotFound,
			pingBody:              "Deployment - Not Found",
			wantErr:               true,
		},
		{
			name:                  "returns error from endpoint",
			pingHandlerStatusCode: http.StatusInternalServerError,
			pingBody:              "Deployment - Not Found",
			wantErr:               true,
		},
		{
			name:                  "returns success on 404 regardless of body wording",
			pingHandlerStatusCode: http.StatusNotFound,
			pingBody:              "any other body",
			wantErr:               false,
		},
		{
			name:                  "returns success from endpoint",
			pingHandlerStatusCode: http.StatusNotFound,
			pingBody:              "Deployment - Not Found",
			wantErr:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			hres := httputil.NewHTTPResp(slog.Default())

			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if tt.pingHandlerDelay != 0 {
					time.Sleep(tt.pingHandlerDelay)
				}

				hres.SendText(r.Context(), w, tt.pingHandlerStatusCode, tt.pingBody)
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"testorg",
				"0123456789abcdef",
				WithRetryAttempts(1),
				WithTimeout(timeout),
				WithPingTimeout(timeout),
			)
			require.NoError(t, err, "Client.HealthCheck() create client unexpected error = %v", err)

			if tt.pingURL != "" {
				c.pingURL = tt.pingURL
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

const testAPIKey = "0123456789abcdef"

func TestClient_redactAPIKey(t *testing.T) {
	t.Parallel()

	c, err := New(
		"https://test.invalid",
		"testorg",
		testAPIKey,
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	require.NoError(t, c.redactAPIKey(nil), "redactAPIKey(nil) should return nil")

	// Errors that do not contain the API key must be returned unchanged.
	plain := errors.New("some transport failure")
	require.ErrorIs(t, c.redactAPIKey(plain), plain, "errors without the key must be passed through unchanged")

	// Errors that contain the API key must have it redacted, while remaining
	// unwrappable to the original error.
	secret := fmt.Errorf("execute request: failed to call https://test.invalid/x/register_impact/%s: boom", testAPIKey)
	got := c.redactAPIKey(secret)
	require.Error(t, got)
	require.NotContains(t, got.Error(), testAPIKey, "redacted error must not contain the api key")
	require.Contains(t, got.Error(), "REDACTED", "redacted error must mention REDACTED")
	require.ErrorIs(t, got, secret, "redacted error must unwrap to the original error")
}

// Test_sendRequest_redactsAPIKey forces a transport error whose message embeds
// the API key (as Go's *url.Error would for endpoints with the key in the URL
// path) and asserts the error surfaced by sendRequest does not leak the secret.
func Test_sendRequest_redactsAPIKey(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	urlWithKey := "https://test.invalid/deployments/testorg/p/e/s/register_impact/" + testAPIKey

	mc := NewMockHTTPClient(ctrl)
	mc.EXPECT().Do(gomock.Any()).Return(nil, &url.Error{
		Op:  "Post",
		URL: urlWithKey,
		Err: errors.New("connection refused"),
	}).Times(1)

	c, err := New(
		"https://test.invalid",
		"testorg",
		testAPIKey,
		WithHTTPClient(mc),
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	req := &DeployRegistrationRequest{
		Deployment: "test_deployment",
		Sha:        "96086c3354a0475073837a24a7fa95a5eb42aab9",
	}

	err = sendRequest(t.Context(), c, urlWithKey, req)

	require.Error(t, err)
	require.NotContains(t, err.Error(), testAPIKey, "sendRequest error must not leak the api key")
	require.Contains(t, err.Error(), "REDACTED", "sendRequest error must redact the api key")
}

// Test_sendRequest_redactsAPIKey_onRequestBuildError forces an
// http.NewRequestWithContext parse failure on a URL embedding the API key and
// asserts the returned error does not leak the secret.
func Test_sendRequest_redactsAPIKey_onRequestBuildError(t *testing.T) {
	t.Parallel()

	c, err := New(
		"https://test.invalid",
		"testorg",
		testAPIKey,
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	req := &DeployRegistrationRequest{
		Deployment: "test_deployment",
		Sha:        "96086c3354a0475073837a24a7fa95a5eb42aab9",
	}

	// The leading "%^*&-ERROR" makes the URL unparsable, so the request-build
	// error message quotes the full URL, including the API-key segment.
	err = sendRequest(t.Context(), c, "%^*&-ERROR/register_impact/"+testAPIKey, req)

	require.Error(t, err)
	require.NotContains(t, err.Error(), testAPIKey, "request-build error must not leak the api key")
	require.Contains(t, err.Error(), "REDACTED", "request-build error must redact the api key")
}

// TestClient_HealthCheck_redactsAPIKey forces a transport error embedding the
// API key on the health-check path and asserts it is redacted before return.
func TestClient_HealthCheck_redactsAPIKey(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	urlWithKey := "https://test.invalid/deployments/testorg/-/register_deploy?key=" + testAPIKey

	mc := NewMockHTTPClient(ctrl)
	mc.EXPECT().Do(gomock.Any()).Return(nil, &url.Error{
		Op:  "Post",
		URL: urlWithKey,
		Err: errors.New("connection refused"),
	}).Times(1)

	c, err := New(
		"https://test.invalid",
		"testorg",
		testAPIKey,
		WithHTTPClient(mc),
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	err = c.HealthCheck(t.Context())

	require.Error(t, err)
	require.NotContains(t, err.Error(), testAPIKey, "HealthCheck error must not leak the api key")
	require.Contains(t, err.Error(), "REDACTED", "HealthCheck error must redact the api key")
}

// errReadCloser is an io.ReadCloser whose Read always fails, used to exercise
// the response-body read error path.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failure") }
func (errReadCloser) Close() error             { return nil }

// TestClient_HealthCheck_bodyReadError returns the expected 404 status but a body
// that fails on read, so HealthCheck reaches the drain step and surfaces the error.
func TestClient_HealthCheck_bodyReadError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mc := NewMockHTTPClient(ctrl)
	mc.EXPECT().Do(gomock.Any()).Return(&http.Response{
		StatusCode: http.StatusNotFound,
		Body:       errReadCloser{},
	}, nil).Times(1)

	c, err := New(
		"https://test.invalid",
		"testorg",
		testAPIKey,
		WithHTTPClient(mc),
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	err = c.HealthCheck(t.Context())

	require.Error(t, err)
	require.ErrorContains(t, err, "failed reading response body")
}

func Test_httpRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		urlStr  string
		req     any
		wantErr bool
	}{
		{
			name:    "fail input validation",
			urlStr:  "https://test.invalid",
			req:     make(chan int), // this payload can't be encoded in JSON
			wantErr: true,
		},
		{
			name:    "fail invalid URL",
			urlStr:  "%^*&-ERROR",
			req:     make(chan int), // this payload can't be encoded in JSON
			wantErr: true,
		},
		{
			name:    "success",
			urlStr:  "https://test.invalid",
			req:     "test",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := httpPostRequest(t.Context(), tt.urlStr, "0123456789abcdef", tt.req)

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

//go:noinline
func newRequestWithContextPatch(_ context.Context, _, _ string, _ io.Reader) (*http.Request, error) {
	return nil, errors.New("ERROR: newRequestWithContextPatch")
}

//nolint:gocognit,paralleltest
func Test_sendRequest(t *testing.T) {
	hres := httputil.NewHTTPResp(slog.Default())

	tests := []struct {
		name              string
		req               *DeployRegistrationRequest
		createMockHandler func(t *testing.T) http.HandlerFunc
		setupMocks        func(client *MockHTTPClient)
		setupPatches      func() (*mpatch.Patch, error)
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
			wantErr: true,
		},
		{
			name: "invalid response status < 200",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, r *http.Request) {
					hres.SendText(r.Context(), w, http.StatusSwitchingProtocols, "")
				}
			},
			wantErr: true,
		},
		{
			name: "fail input validation",
			req: &DeployRegistrationRequest{
				Deployment: "test_deployment_error",
			},
			wantErr: true,
		},
		{
			name: "success valid response",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, r *http.Request) {
					hres.SendText(r.Context(), w, http.StatusOK, "Success")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip mpatch-based testcases on Apple Silicon macOS
			if tt.setupPatches != nil && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				t.Skip("mpatch not supported on Mac silicon - skipping test")
			}

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			urlTestPath := "/test"

			mux := http.NewServeMux()
			if tt.createMockHandler != nil {
				mux.HandleFunc(urlTestPath, tt.createMockHandler(t))
			}

			ts := httptest.NewServer(mux)
			defer ts.Close()

			clientOpts := []Option{}

			if tt.setupMocks != nil {
				mc := NewMockHTTPClient(ctrl)
				tt.setupMocks(mc)
				clientOpts = append(clientOpts, WithHTTPClient(mc), WithRetryAttempts(1))
			}

			c, err := New(
				ts.URL,
				"testorg",
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
				tt.req = &DeployRegistrationRequest{
					Deployment: "test_deployment",
					Sha:        "96086c3354a0475073837a24a7fa95a5eb42aab9",
				}
			}

			err = sendRequest(t.Context(), c, ts.URL+urlTestPath, tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

func getTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	hres := httputil.NewHTTPResp(slog.Default())

	createMockHandler := func(t *testing.T) http.HandlerFunc {
		t.Helper()

		return func(w http.ResponseWriter, r *http.Request) {
			hres.SendText(r.Context(), w, http.StatusOK, "Success")
		}
	}

	mux.HandleFunc("/", createMockHandler(t))

	return httptest.NewServer(mux)
}

func TestClient_SendDeployRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *DeployRegistrationRequest
		wantErr bool
	}{
		{
			name:    "fail with empty request",
			req:     &DeployRegistrationRequest{},
			wantErr: true,
		},
		{
			name: "fail with invalid tags",
			req: &DeployRegistrationRequest{
				Deployment: "test_deployment",
				Sha:        "96086c3354a0475073837a24a7fa95a5eb42aab9",
				Tags: []string{
					"alpha",
					"beta",
				},
			},
			wantErr: true,
		},
		{
			name: "success with required fields",
			req: &DeployRegistrationRequest{
				Deployment: "test_deployment",
				Sha:        "96086c3354a0475073837a24a7fa95a5eb42aab9",
			},
			wantErr: false,
		},
		{
			name: "success with all fields set",
			req: &DeployRegistrationRequest{
				Deployment:  "test_deployment",
				Sha:         "96086c3354a0475073837a24a7fa95a5eb42aab9",
				Environment: "test",
				Date:        "2023-04-24 12:20:00",
				Tags: []string{
					"#alpha",
					"#beta",
				},
				IgnoreIfDuplicate: true,
				Email:             "test@example.invalid",
				Links: map[string]string{
					"one": "https://test.one.invalid",
					"two": "https://test.two.invalid",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := getTestServer(t)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"testorg",
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendDeployRegistration(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

func TestClient_SendManualChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *ManualChangeRequest
		wantErr bool
	}{
		{
			name:    "fail with empty request",
			req:     &ManualChangeRequest{},
			wantErr: true,
		},
		{
			name: "fail with invalid tags",
			req: &ManualChangeRequest{
				Project: "test_project",
				Name:    "test_name",
				Tags: []string{
					"alpha",
					"beta",
				},
			},
			wantErr: true,
		},
		{
			name: "success with required fields",
			req: &ManualChangeRequest{
				Project: "test_project",
				Name:    "test_name",
			},
			wantErr: false,
		},
		{
			name: "success with all fields set",
			req: &ManualChangeRequest{
				Project:     "test_project",
				Name:        "test_name",
				Description: "test_description",
				Environment: "test",
				Tags: []string{
					"#alpha",
					"#beta",
				},
				Author: "author@example.invalid",
				Email:  "test@example.invalid",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := getTestServer(t)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"testorg",
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendManualChange(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

// TestClient_SendManualChange_authorJSONKey verifies the Author field is
// serialized under the lowercase "author" key expected by the Sleuth API.
func TestClient_SendManualChange_authorJSONKey(t *testing.T) {
	t.Parallel()

	bodyCh := make(chan []byte, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyCh <- body

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := New(
		ts.URL,
		"testorg",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	err = c.SendManualChange(t.Context(), &ManualChangeRequest{
		Project: "test_project",
		Name:    "test_name",
		Author:  "author@example.invalid",
	})
	require.NoError(t, err)

	body := string(<-bodyCh)

	require.Contains(t, body, `"author":"author@example.invalid"`, "Author must serialize under the lowercase author key")
	require.NotContains(t, body, `"Author"`, "the Go field name must not be used as the JSON key")
}

// TestClient_pathSegmentsEscaped verifies that dynamic path segments
// containing reserved URL characters are percent-escaped, so they stay a
// single path segment instead of rewriting the request path.
func TestClient_pathSegmentsEscaped(t *testing.T) {
	t.Parallel()

	uriCh := make(chan string, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uriCh <- r.RequestURI

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := New(
		ts.URL,
		"testorg",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	err = c.SendDeployRegistration(t.Context(), &DeployRegistrationRequest{
		Deployment: "abc/../def?x=1",
		Sha:        "96086c3354a0475073837a24a7fa95a5eb42aab9",
	})
	require.NoError(t, err)

	uri := <-uriCh

	require.Equal(t, "/deployments/testorg/abc%2F..%2Fdef%3Fx=1/register_deploy", uri,
		"the deployment segment must be percent-escaped into a single path segment")
}

func TestClient_SendCustomIncidentImpactRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *CustomIncidentImpactRegistrationRequest
		wantErr bool
	}{
		{
			name:    "fail with empty request",
			req:     &CustomIncidentImpactRegistrationRequest{},
			wantErr: true,
		},
		{
			name: "fail with invalid type",
			req: &CustomIncidentImpactRegistrationRequest{
				Project:      "test_project",
				Environment:  "test",
				ImpactSource: "test_impact_source",
				Type:         "invalid",
			},
			wantErr: true,
		},
		{
			name: "success with required fields",
			req: &CustomIncidentImpactRegistrationRequest{
				Project:      "test_project",
				Environment:  "test",
				ImpactSource: "test_impact_source",
				Type:         Triggered,
			},
			wantErr: false,
		},
		{
			name: "success with all fields set",
			req: &CustomIncidentImpactRegistrationRequest{
				Project:      "test_project",
				Environment:  "test",
				ImpactSource: "test_impact_source",
				Type:         Triggered,
				ID:           "abcdef0123456789",
				Date:         "2023-04-24 13:00:00",
				EndedDate:    "2023-04-24 14:10:00",
				Title:        "test_incident_title",
				URL:          "http://test.external.url.invalid",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := getTestServer(t)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"testorg",
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendCustomIncidentImpactRegistration(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

func TestClient_SendCustomMetricImpactRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *CustomMetricImpactRegistrationRequest
		wantErr bool
	}{
		{
			name:    "fail with empty request",
			req:     &CustomMetricImpactRegistrationRequest{},
			wantErr: true,
		},
		{
			name: "fail with invalid date",
			req: &CustomMetricImpactRegistrationRequest{
				ImpactID: 3451,
				Value:    123.4561,
				Date:     "error_date",
			},
			wantErr: true,
		},
		{
			name: "success with required fields",
			req: &CustomMetricImpactRegistrationRequest{
				ImpactID: 3452,
				Value:    123.4562,
			},
			wantErr: false,
		},
		{
			name: "success with legitimate zero value",
			req: &CustomMetricImpactRegistrationRequest{
				ImpactID: 3454,
				Value:    0,
			},
			wantErr: false,
		},
		{
			name: "success with all fields set",
			req: &CustomMetricImpactRegistrationRequest{
				ImpactID: 3453,
				Value:    123.4563,
				Date:     "2023-04-24 13:14:15",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := getTestServer(t)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"testorg",
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendCustomMetricImpactRegistration(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

// TestClient_Send_nilRequest verifies each send method rejects a nil request
// with ErrNilRequest instead of panicking on a nil-pointer dereference.
func TestClient_Send_nilRequest(t *testing.T) {
	t.Parallel()

	c, err := New("https://app.sleuth.invalid", "testorg", "0123456789abcdef", WithRetryAttempts(1))
	require.NoError(t, err)

	require.ErrorIs(t, c.SendDeployRegistration(t.Context(), nil), ErrNilRequest)
	require.ErrorIs(t, c.SendManualChange(t.Context(), nil), ErrNilRequest)
	require.ErrorIs(t, c.SendCustomIncidentImpactRegistration(t.Context(), nil), ErrNilRequest)
	require.ErrorIs(t, c.SendCustomMetricImpactRegistration(t.Context(), nil), ErrNilRequest)
}

// TestNew_sentinelErrors verifies New wraps its failures in the exported
// sentinels so callers can match them with errors.Is.
func TestNew_sentinelErrors(t *testing.T) {
	t.Parallel()

	_, err := New("http://", "testorg", "0123456789abcdef", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrInvalidAddress)

	_, err = New("http://service.domain.invalid:1234", "", "0123456789abcdef", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrEmptyOrg)

	_, err = New("http://service.domain.invalid:1234", "testorg", "", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrEmptyAPIKey)

	_, err = New("http://service.domain.invalid:1234", "testorg", "0123456789abcdef", WithRetryAttempts(0))
	require.ErrorIs(t, err, ErrInvalidRetryConfig)
}
