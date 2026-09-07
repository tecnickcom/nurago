//go:generate mockgen -write_package_comment=false -package devlake -destination ./mock_test.go . HTTPClient
package devlake

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
		apikey      string
		opts        []Option
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:    "fails with invalid character in URL",
			addr:    "http://invalid-url.domain.invalid\u007F",
			apikey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with empty address",
			addr:    "",
			apikey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with relative address missing scheme and host",
			addr:    "/api/rest",
			apikey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with scheme but missing host",
			addr:    "http://",
			apikey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "fails with empty api key",
			addr:    "http://service.domain.invalid:1234",
			apikey:  "",
			wantErr: true,
		},
		{
			name:        "succeeds with defaults",
			addr:        "http://service.domain.invalid:1234",
			apikey:      "0123456789abcdef",
			wantTimeout: defaultPingTimeout,
			wantErr:     false,
		},
		{
			name:        "succeeds with options",
			addr:        "http://service.domain.invalid:1234",
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

// TestNew_trailingSlashAddr verifies that a trailing slash in addr does not
// produce "//" in the endpoint URLs (they are built with URL.JoinPath).
func TestNew_trailingSlashAddr(t *testing.T) {
	t.Parallel()

	c, err := New(
		"https://app.devlake.invalid/",
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	base := "https://app.devlake.invalid"

	require.Equal(t, base+"/api/rest/version", c.pingURL)
	require.Equal(t, base+"/api/rest/plugins/webhook/connections/%d/deployments", c.deploymentRegURLFormat)
	require.Equal(t, base+"/api/rest/plugins/webhook/%d/issues", c.incidentRegURLFormat)
	require.Equal(t, base+"/api/rest/plugins/webhook/%d/issue/%s/close", c.incidentCloseURLFormat)
}

//nolint:paralleltest // mutates the package-level newValidator seam
func TestNew_validatorError(t *testing.T) {
	orig := newValidator

	t.Cleanup(func() { newValidator = orig })

	newValidator = func(...validator.Option) (*validator.Validator, error) {
		return nil, errors.New("test-error")
	}

	c, err := New("http://service.domain.invalid:1234", "0123456789abcdef")

	require.Error(t, err)
	require.Nil(t, c)
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
		bodyErr               bool
		wantErr               bool
	}{
		{
			name:                  "fails because ping url error",
			pingHandlerStatusCode: http.StatusOK,
			pingURL:               "%^*&-ERROR",
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

				hres.SendText(r.Context(), w, tt.pingHandlerStatusCode, `{"version":"v1.0.2-beta1@2e768b5"}`)
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

func TestNew_invalidRetryConfig(t *testing.T) {
	t.Parallel()

	// The retrier is built during New, so an invalid retry setting (0 attempts)
	// is rejected at construction instead of failing on every request.
	c, err := New(
		"https://test.invalid",
		"0123456789abcdef",
		WithRetryAttempts(0),
	)

	require.Nil(t, c, "New() returned client should be nil")
	require.Error(t, err, "New() should fail with invalid retry attempts")
}

func Test_httpPostRequest(t *testing.T) {
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

func getValidDeploymentReq() *DeploymentRequest {
	nowdate := time.Now()

	return &DeploymentRequest{
		ConnectionID: 1,
		ID:           "one",
		DisplayTitle: "deploy",
		Result:       "SUCCESS",
		Environment:  "TESTING",
		Name:         "test_deployment",
		URL:          "https://cicd.invalid/test/deployment/1234",
		CreatedDate:  &nowdate,
		StartedDate:  &nowdate,
		FinishedDate: &nowdate,
		DeploymentCommits: []DeploymentCommitsRequest{
			{
				DisplayTitle: "commit_title",
				RepoID:       "repo_id",
				RepoURL:      "https://cvs.invalid/repo/name",
				Name:         "commit_name",
				RefName:      "ref_name",
				CommitSha:    "b3633555db2f1ebb42712403e4a4603709012f23",
				CommitMsg:    "test commit message",
				Result:       "SUCCESS",
				Status:       "status",
				CreatedDate:  &nowdate,
				StartedDate:  &nowdate,
				FinishedDate: &nowdate,
			},
		},
	}
}

//nolint:gocognit,paralleltest
func Test_sendRequest(t *testing.T) {
	hres := httputil.NewHTTPResp(slog.Default())

	tests := []struct {
		name              string
		req               *DeploymentRequest
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
			req: &DeploymentRequest{
				Environment: "WRONG_VALUE",
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
				tt.req = getValidDeploymentReq()
			}

			err = sendRequest(t.Context(), c, ts.URL+urlTestPath, tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

func getTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	hres := httputil.NewHTTPResp(slog.Default())
	mux := http.NewServeMux()

	createMockHandler := func(t *testing.T) http.HandlerFunc {
		t.Helper()

		return func(w http.ResponseWriter, r *http.Request) {
			hres.SendText(r.Context(), w, http.StatusOK, "Success")
		}
	}

	mux.HandleFunc("/", createMockHandler(t))

	return httptest.NewServer(mux)
}

func TestClient_SendDeployment(t *testing.T) {
	t.Parallel()

	nowdate := time.Now()

	tests := []struct {
		name    string
		req     *DeploymentRequest
		wantErr bool
	}{
		{
			name:    "fail with missing required field",
			req:     &DeploymentRequest{},
			wantErr: true,
		},
		{
			name: "fail with empty ID",
			req: &DeploymentRequest{
				ConnectionID: 1,
				ID:           "",
				StartedDate:  &nowdate,
				FinishedDate: &nowdate,
			},
			wantErr: true,
		},
		{
			name: "success with required fields",
			req: &DeploymentRequest{
				ConnectionID: 2,
				ID:           "id",
				StartedDate:  &nowdate,
				FinishedDate: &nowdate,
			},
			wantErr: false,
		},
		{
			name:    "success with all fields set",
			req:     getValidDeploymentReq(),
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
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendDeployment(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

func getValidIncidentRequest() *IncidentRequest {
	nowdate := time.Now()

	return &IncidentRequest{
		ConnectionID:            3,
		URL:                     "https://incident.invalid/data/1234",
		IssueKey:                "issue_key",
		Title:                   "issue_title",
		Description:             "issue_description",
		EpicKey:                 "epic_key",
		Type:                    "INCIDENT",
		Status:                  "IN_PROGRESS",
		OriginalStatus:          "open",
		StoryPoint:              5,
		ResolutionDate:          &nowdate,
		CreatedDate:             &nowdate,
		UpdatedDate:             &nowdate,
		LeadTimeMinutes:         33,
		ParentIssueKey:          "parent_issue_key",
		Priority:                "priority",
		OriginalEstimateMinutes: 57,
		TimeSpentMinutes:        53,
		TimeRemainingMinutes:    4,
		CreatorID:               "creator_id",
		CreatorName:             "creator_name",
		AssigneeID:              "assignee_id",
		AssigneeName:            "assignee_name",
		Severity:                "severity",
		Component:               "component",
	}
}

func TestClient_SendIncident(t *testing.T) {
	t.Parallel()

	nowdate := time.Now()

	tests := []struct {
		name    string
		req     *IncidentRequest
		wantErr bool
	}{
		{
			name:    "fail with empty request",
			req:     &IncidentRequest{},
			wantErr: true,
		},
		{
			name: "fail with invalid type",
			req: &IncidentRequest{
				ConnectionID:   3,
				URL:            "~'~%",
				IssueKey:       "issue_key",
				Title:          "issue_title",
				Status:         "IN_PROGRESS",
				OriginalStatus: "open",
				CreatedDate:    &nowdate,
			},
			wantErr: true,
		},
		{
			name: "success with required fields",
			req: &IncidentRequest{
				ConnectionID:   3,
				IssueKey:       "issue_key",
				Title:          "issue_title",
				Status:         "IN_PROGRESS",
				OriginalStatus: "open",
				CreatedDate:    &nowdate,
			},
			wantErr: false,
		},
		{
			name:    "success with all fields set",
			req:     getValidIncidentRequest(),
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
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendIncident(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

// getEmptyBodyTestServer returns a stub server that asserts the incoming POST
// request body is empty (not the literal "null" produced by encoding a nil
// payload) before responding with success.
func getEmptyBodyTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	hres := httputil.NewHTTPResp(slog.Default())
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, rerr := io.ReadAll(r.Body)
		// assert (not require) because this runs in the server goroutine.
		assert.NoError(t, rerr, "reading close request body")
		assert.Empty(t, body, "close request body must be empty, got %q", string(body))

		hres.SendText(r.Context(), w, http.StatusOK, "Success")
	})

	return httptest.NewServer(mux)
}

func TestClient_SendIncidentClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *IncidentRequestClose
		wantErr bool
	}{
		{
			name:    "fail with empty request",
			req:     &IncidentRequestClose{},
			wantErr: true,
		},
		{
			name: "fail with empty issueKey",
			req: &IncidentRequestClose{
				ConnectionID: 3,
				IssueKey:     "",
			},
			wantErr: true,
		},
		{
			name: "success with all fields set",
			req: &IncidentRequestClose{
				ConnectionID: 3,
				IssueKey:     "issue_key",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := getEmptyBodyTestServer(t)
			defer ts.Close()

			c, err := New(
				ts.URL,
				"0123456789abcdef",
				WithRetryAttempts(1),
			)
			require.NoError(t, err)

			err = c.SendIncidentClose(t.Context(), tt.req)
			require.Equal(t, tt.wantErr, err != nil, "error: %v", err)
		})
	}
}

// TestClient_SendIncidentClose_escapesIssueKey verifies that an issue key
// containing reserved URL characters is percent-escaped into a single path
// segment, so it cannot rewrite the path or swallow the "/close" suffix.
func TestClient_SendIncidentClose_escapesIssueKey(t *testing.T) {
	t.Parallel()

	uriCh := make(chan string, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uriCh <- r.RequestURI

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := New(
		ts.URL,
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	err = c.SendIncidentClose(t.Context(), &IncidentRequestClose{
		ConnectionID: 3,
		IssueKey:     "PROJ/123?x=1",
	})
	require.NoError(t, err)

	uri := <-uriCh

	require.Equal(t, "/api/rest/plugins/webhook/3/issue/PROJ%2F123%3Fx=1/close", uri,
		"the issue key must be percent-escaped into a single path segment")
}

func TestClient_SendIncidentClose_newRequestError(t *testing.T) {
	t.Parallel()

	ts := getEmptyBodyTestServer(t)
	defer ts.Close()

	c, err := New(
		ts.URL,
		"0123456789abcdef",
		WithRetryAttempts(1),
	)
	require.NoError(t, err)

	// Force http.NewRequestWithContext to fail by injecting a URL format that
	// expands to an unparseable URL (the leading "%^*&-ERROR" is invalid).
	c.incidentCloseURLFormat = "%%^*&-ERROR/%d/%s"

	err = c.SendIncidentClose(t.Context(), &IncidentRequestClose{
		ConnectionID: 3,
		IssueKey:     "issue_key",
	})
	require.Error(t, err)
}

// TestClient_Send_nilRequest verifies each send method rejects a nil request
// with ErrNilRequest instead of panicking on a nil-pointer dereference.
func TestClient_Send_nilRequest(t *testing.T) {
	t.Parallel()

	c, err := New("https://app.devlake.invalid", "0123456789abcdef", WithRetryAttempts(1))
	require.NoError(t, err)

	require.ErrorIs(t, c.SendDeployment(t.Context(), nil), ErrNilRequest)
	require.ErrorIs(t, c.SendIncident(t.Context(), nil), ErrNilRequest)
	require.ErrorIs(t, c.SendIncidentClose(t.Context(), nil), ErrNilRequest)
}

// TestNew_sentinelErrors verifies New wraps its failures in the exported
// sentinels so callers can match them with errors.Is.
func TestNew_sentinelErrors(t *testing.T) {
	t.Parallel()

	_, err := New("http://", "0123456789abcdef", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrInvalidAddress)

	_, err = New("http://service.domain.invalid:1234", "", WithRetryAttempts(1))
	require.ErrorIs(t, err, ErrEmptyAPIKey)

	_, err = New("http://service.domain.invalid:1234", "0123456789abcdef", WithRetryAttempts(0))
	require.ErrorIs(t, err, ErrInvalidRetryConfig)
}
