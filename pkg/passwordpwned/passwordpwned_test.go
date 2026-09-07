//go:generate mockgen -write_package_comment=false -package passwordpwned -destination ./mock_test.go . HTTPClient
package passwordpwned

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/undefinedlabs/go-mpatch"
	"go.uber.org/mock/gomock"
)

//go:noinline
func newRequestWithContextPatch(_ context.Context, _, _ string, _ io.Reader) (*http.Request, error) {
	return nil, errors.New("error")
}

//nolint:gocognit,tparallel
func TestClient_IsPwnedPassword(t *testing.T) {
	t.Parallel()

	// pwned.password.1 : AC8A89B5F24DE5F1D9AE8499A204B5098B08DF1B
	// pwned.password.2 : 274AC46FA9F7FDDB8AB4A5BB8295A47E3929171E
	// pwned.password.3 : C1C39EBC8981022DC3220FF6C17D1933BA5E5061
	// pwned.password.4 : 05955AE4E6ADFB93265CA2BCF0560529CF0BFDC9
	// pwned.password.5 : 34D03CE275F04C48AF10A4E23AB85D27AF3239B0
	// pwned.password.6 : ACE9846F1DC7F76EB2E5D064BDEFE65B712F85D3

	// body:
	// 9B5F24DE5F1D9AE8499A204B5098B08DF1B:1
	// 46FA9F7FDDB8AB4A5BB8295A47E3929171E:2
	// EBC8981022DC3220FF6C17D1933BA5E5061:3
	// AE4E6ADFB93265CA2BCF0560529CF0BFDC9:4
	// CE275F04C48AF10A4E23AB85D27AF3239B0:0
	// 46F1DC7F76EB2E5D064BDEFE65B712F85D3:0

	// base64 brotli encoded body
	retBody, _ := base64.StdEncoding.DecodeString("G+IA+I2ULm8UPTY2L7T4yFAoTZILH26i9Ehm9XAi90lEEkgpxCt4c1gfxS7j/GbqZUlq1aPQFF8OCnTcT1v94iEQTTMR3FmjDwZzpa6C4edcWcu5CibTTqo+UAOl6IO66fjSS64H0vLyEFKWOOvpkOcxcRR8EDuc9nPbotUBk5q9NS1HOvwB")

	mockHandleFn := func(t *testing.T, body []byte) http.HandlerFunc {
		t.Helper()

		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", httputil.MimeTextPlain)
			w.Header().Set("Content-Encoding", "br")
			_, err := w.Write(body)
			assert.NoError(t, err)
		}
	}

	tests := []struct {
		name              string
		password          string
		createMockHandler func(t *testing.T) http.HandlerFunc
		setupMocks        func(client *MockHTTPClient)
		setupPatches      func() (*mpatch.Patch, error)
		pwned             bool
		wantErr           bool
	}{
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
			name: "failed to execute request - transport error",
			setupMocks: func(m *MockHTTPClient) {
				m.EXPECT().Do(gomock.Any()).Return(nil, errors.New("transport error")).Times(1)
			},
			wantErr: true,
		},
		{
			name: "unexpected http error status code",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}
			},
			wantErr: true,
		},
		{
			name: "invalid response status < 200",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusSwitchingProtocols)
				}
			},
			wantErr: true,
		},
		{
			name:     "invalid brotli encoding",
			password: "pwned.password.1", // AC8A89B5F24DE5F1D9AE8499A204B5098B08DF1B
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()
				return mockHandleFn(t, []byte("invalid"))
			},
			pwned:   false,
			wantErr: true,
		},
		{
			name:     "pwned password",
			password: "pwned.password.2", // 274AC46FA9F7FDDB8AB4A5BB8295A47E3929171E
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()
				return mockHandleFn(t, retBody)
			},
			pwned:   true,
			wantErr: false,
		},
		{
			name:     "false pwned password because of padding",
			password: "pwned.password.6", // ACE9846F1DC7F76EB2E5D064BDEFE65B712F85D3
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()
				return mockHandleFn(t, retBody)
			},
			pwned:   false,
			wantErr: false,
		},
		{ //nolint:gosec
			name:     "ok password",
			password: "not.pwned.password",
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()
				return mockHandleFn(t, retBody)
			},
			pwned:   false,
			wantErr: false,
		},
		{
			name:     "malformed response - truncated after suffix",
			password: "pwned.password.2", // 274AC46FA9F7FDDB8AB4A5BB8295A47E3929171E
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				// brotli("46FA9F7FDDB8AB4A5BB8295A47E3929171E"): the matched
				// suffix ends the body with no ":<count>" following it.
				body, err := base64.StdEncoding.DecodeString("HyIA+EVPPW9Ww9kGJQpGwcDhH/vyZlQUGFJ5B5H7mqUt")
				require.NoError(t, err)

				return mockHandleFn(t, body)
			},
			pwned:   false,
			wantErr: true,
		},
		{
			name:     "malformed response - wrong separator",
			password: "pwned.password.2", // 274AC46FA9F7FDDB8AB4A5BB8295A47E3929171E
			createMockHandler: func(t *testing.T) http.HandlerFunc {
				t.Helper()

				// brotli("46FA9F7FDDB8AB4A5BB8295A47E3929171E;2"): the matched
				// suffix is followed by ';' instead of the expected ':'.
				body, err := base64.StdEncoding.DecodeString("HyQA+EXPpHH2HXJTG5QsGAUD5+/QlzfDAh1BlnbAc62RlP0K")
				require.NoError(t, err)

				return mockHandleFn(t, body)
			},
			pwned:   false,
			wantErr: true,
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

			mux := http.NewServeMux()
			if tt.createMockHandler != nil {
				mux.HandleFunc("/"+rangePath+"/", tt.createMockHandler(t))
			}

			ts := httptest.NewServer(mux)
			defer ts.Close()

			clientOpts := []Option{
				WithURL(ts.URL),
				WithRetryAttempts(1),
			}

			if tt.setupMocks != nil {
				mc := NewMockHTTPClient(ctrl)
				tt.setupMocks(mc)
				clientOpts = append(clientOpts, WithHTTPClient(mc), WithRetryAttempts(1))
			}

			c, err := New(clientOpts...)
			require.NoError(t, err)

			if tt.setupPatches != nil {
				patch, err := tt.setupPatches()
				require.NoError(t, err)

				defer func() {
					_ = patch.Unpatch()
				}()
			}

			got, err := c.IsPwnedPassword(t.Context(), tt.password)

			require.Equal(t, tt.wantErr, err != nil, err)
			require.Equal(t, tt.pwned, got)
		})
	}
}

// TestClient_IsPwnedPassword_Concurrent verifies that concurrent
// IsPwnedPassword calls on a shared Client are race-free (run with -race) and
// return consistent results: the SHA-1 hash is computed locally per call
// instead of through shared hash state.
//
//nolint:paralleltest // TestClient_IsPwnedPassword mpatch-es process-global functions in the parallel phase.
func TestClient_IsPwnedPassword_Concurrent(t *testing.T) {
	// Same brotli-encoded body used by TestClient_IsPwnedPassword.
	retBody, err := base64.StdEncoding.DecodeString("G+IA+I2ULm8UPTY2L7T4yFAoTZILH26i9Ehm9XAi90lEEkgpxCt4c1gfxS7j/GbqZUlq1aPQFF8OCnTcT1v94iEQTTMR3FmjDwZzpa6C4edcWcu5CibTTqo+UAOl6IO66fjSS64H0vLyEFKWOOvpkOcxcRR8EDuc9nPbotUBk5q9NS1HOvwB")
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+rangePath+"/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", httputil.MimeTextPlain)
		w.Header().Set("Content-Encoding", "br")
		_, werr := w.Write(retBody)
		assert.NoError(t, werr)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	c, err := New(WithURL(ts.URL), WithRetryAttempts(1))
	require.NoError(t, err)

	tests := []struct {
		password string
		pwned    bool
	}{
		{password: "pwned.password.1", pwned: true},
		{password: "pwned.password.2", pwned: true},
		{password: "pwned.password.5", pwned: false},   // zero recurrence
		{password: "not.pwned.password", pwned: false}, //nolint:gosec // test data, not a credential
	}

	var wg sync.WaitGroup

	for range 8 {
		for _, tt := range tests {
			wg.Go(func() {
				got, gerr := c.IsPwnedPassword(t.Context(), tt.password)
				assert.NoError(t, gerr, tt.password) //nolint:testifylint // require must not be called from a non-test goroutine
				assert.Equal(t, tt.pwned, got, tt.password)
			})
		}
	}

	wg.Wait()
}
