//go:generate mockgen -write_package_comment=false -package httpretrier -destination ./mock_test.go . HTTPClient
package httpretrier

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/testutil"
	"go.uber.org/mock/gomock"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []Option
		wantErr bool
	}{
		{
			name:    "succeeds with defaults",
			wantErr: false,
		},
		{
			name: "succeeds with custom values",
			opts: []Option{
				WithRetryIfFn(func(_ *http.Response, _ error) bool { return true }),
				WithAttempts(5),
				WithDelay(601 * time.Millisecond),
				WithDelayFactor(1.3),
				WithJitter(109 * time.Millisecond),
			},
			wantErr: false,
		},
		{
			name: "succeeds with RetryIfForWriteRequests",
			opts: []Option{
				WithRetryIfFn(RetryIfForWriteRequests),
			},
			wantErr: false,
		},
		{
			name: "succeeds with RetryIfForReadRequests",
			opts: []Option{
				WithRetryIfFn(RetryIfForReadRequests),
			},
			wantErr: false,
		},
		{
			name:    "fails with invalid option",
			opts:    []Option{WithJitter(0)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(http.DefaultClient, tt.opts...)

			if tt.wantErr {
				require.Nil(t, c, "New() returned value should be nil")
				require.Error(t, err, "New() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			require.NotNil(t, c, "New() returned value should not be nil")
			require.NoError(t, err, "New() unexpected error = %v", err)
		})
	}
}

func Test_defaultRetryIf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "true with error",
			err:  errors.New("ERROR"),
			want: true,
		},
		{
			name: "false with no error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := defaultRetryIf(nil, tt.err)

			require.Equal(t, tt.want, got)
		})
	}
}

func TestRetryIfForWriteRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{
			name:   "true with error",
			status: http.StatusOK,
			err:    errors.New("ERROR"),
			want:   true,
		},
		{
			name:   "true with http.StatusTooManyRequests",
			status: http.StatusTooManyRequests,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusBadGateway",
			status: http.StatusBadGateway,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusServiceUnavailable",
			status: http.StatusServiceUnavailable,
			err:    nil,
			want:   true,
		},
		{
			name:   "false with no matching status code",
			status: http.StatusOK,
			err:    nil,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response := &http.Response{
				Status:     http.StatusText(tt.status),
				StatusCode: tt.status,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}
			got := RetryIfForWriteRequests(response, tt.err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRetryIfForReadRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{
			name:   "true with error",
			status: http.StatusOK,
			err:    errors.New("ERROR"),
			want:   true,
		},
		{
			name:   "true with http.StatusNotFound",
			status: http.StatusNotFound,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusRequestTimeout",
			status: http.StatusRequestTimeout,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusConflict",
			status: http.StatusConflict,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusLocked",
			status: http.StatusLocked,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusTooEarly",
			status: http.StatusTooEarly,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusTooManyRequests",
			status: http.StatusTooManyRequests,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusInternalServerError",
			status: http.StatusInternalServerError,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusBadGateway",
			status: http.StatusBadGateway,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusServiceUnavailable",
			status: http.StatusServiceUnavailable,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusGatewayTimeout",
			status: http.StatusGatewayTimeout,
			err:    nil,
			want:   true,
		},
		{
			name:   "true with http.StatusInsufficientStorage",
			status: http.StatusInsufficientStorage,
			err:    nil,
			want:   true,
		},
		{
			name:   "false with no matching status code",
			status: http.StatusOK,
			err:    nil,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response := &http.Response{
				Status:     http.StatusText(tt.status),
				StatusCode: tt.status,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}
			got := RetryIfForReadRequests(response, tt.err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRetryIfFnByHTTPMethod(t *testing.T) {
	t.Parallel()

	testResp := &http.Response{}
	testErr := errors.New("sample error")

	tests := []struct {
		name       string
		httpMethod string
		want       RetryIfFn
	}{
		{
			name:       "GET method",
			httpMethod: http.MethodGet,
			want:       RetryIfForReadRequests,
		},
		{
			name:       "POST method",
			httpMethod: http.MethodPost,
			want:       RetryIfForWriteRequests,
		},
		{
			name:       "PUT method",
			httpMethod: http.MethodPut,
			want:       RetryIfForWriteRequests,
		},
		{
			name:       "PATCH method",
			httpMethod: http.MethodPatch,
			want:       RetryIfForWriteRequests,
		},
		{
			name:       "DELETE method",
			httpMethod: http.MethodDelete,
			want:       RetryIfForWriteRequests,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := RetryIfFnByHTTPMethod(tt.httpMethod)

			require.Equal(t, tt.want(testResp, testErr), got(testResp, testErr))
		})
	}
}

func TestRetryIf_nilResponse(t *testing.T) {
	t.Parallel()

	// Exported policies must not panic on a nil response (e.g. a non-conforming
	// client returning (nil, nil), or a direct caller passing nil).
	require.False(t, RetryIfForReadRequests(nil, nil))
	require.True(t, RetryIfForReadRequests(nil, errors.New("x")))
	require.False(t, RetryIfForWriteRequests(nil, nil))
	require.True(t, RetryIfForWriteRequests(nil, errors.New("x")))
}

func TestRetryAfterDelay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	const defMax = DefaultMaxRetryAfter

	tests := []struct {
		name    string
		nilResp bool
		header  string // Retry-After value; empty means no header
		maxRA   time.Duration
		wantDur time.Duration
		wantOk  bool
	}{
		{"nil response", true, "", defMax, 0, false},
		{"no header", false, "", defMax, 0, false},
		{"delta seconds", false, "5", defMax, 5 * time.Second, true},
		{"zero seconds", false, "0", defMax, 0, false},
		{"negative seconds", false, "-3", defMax, 0, false},
		{"huge seconds capped by default", false, "999999", defMax, defMax, true},
		{"delta capped by custom max", false, "100", 10 * time.Second, 10 * time.Second, true},
		{"http-date future", false, now.Add(10 * time.Second).Format(http.TimeFormat), defMax, 10 * time.Second, true},
		{"http-date past", false, now.Add(-10 * time.Second).Format(http.TimeFormat), defMax, 0, false},
		{"http-date far future capped by default", false, now.Add(48 * time.Hour).Format(http.TimeFormat), defMax, defMax, true},
		{"http-date capped by custom max", false, now.Add(time.Hour).Format(http.TimeFormat), 10 * time.Minute, 10 * time.Minute, true},
		{"malformed", false, "not-a-date", defMax, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var resp *http.Response

			if !tt.nilResp {
				h := http.Header{}
				if tt.header != "" {
					h.Set("Retry-After", tt.header)
				}

				resp = &http.Response{Header: h, Body: io.NopCloser(bytes.NewReader(nil))}
				defer func() { _ = resp.Body.Close() }()
			}

			d, ok := retryAfterDelay(resp, now, tt.maxRA)
			require.Equal(t, tt.wantOk, ok)
			require.Equal(t, tt.wantDur, d)
		})
	}
}

//nolint:gocognit
func TestHTTPRetrier_Do(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		setupMocks       func(mock *MockHTTPClient)
		ctxTimeout       time.Duration
		body             io.Reader
		wantStatus       int
		wantErr          bool
		requestBodyError bool
	}{
		{
			name: "success at first attempt",
			setupMocks: func(mock *MockHTTPClient) {
				rOK := &http.Response{
					Status:     http.StatusText(http.StatusOK),
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}
				mock.EXPECT().Do(gomock.Any()).Return(rOK, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "success at first attempt with body",
			setupMocks: func(mock *MockHTTPClient) {
				rOK := &http.Response{
					Status:     http.StatusText(http.StatusOK),
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}
				mock.EXPECT().Do(gomock.Any()).Return(rOK, nil)
			},
			body:       bytes.NewReader([]byte(`some body`)),
			wantStatus: http.StatusOK,
		},
		{
			name: "success at third attempt after multiple retry conditions",
			setupMocks: func(mock *MockHTTPClient) {
				rErr := &http.Response{
					Status:     http.StatusText(http.StatusInternalServerError),
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}
				rOK := &http.Response{
					Status:     http.StatusText(http.StatusOK),
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}

				mock.EXPECT().Do(gomock.Any()).Return(nil, errors.New("network error"))
				mock.EXPECT().Do(gomock.Any()).Return(rErr, nil)
				mock.EXPECT().Do(gomock.Any()).Return(rOK, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "fail all attempts",
			setupMocks: func(mock *MockHTTPClient) {
				rErr := &http.Response{
					Status:     http.StatusText(http.StatusInternalServerError),
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}

				mock.EXPECT().Do(gomock.Any()).Return(nil, errors.New("network error"))
				mock.EXPECT().Do(gomock.Any()).Return(rErr, nil).Times(3)
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "request context timeout",
			setupMocks: func(mock *MockHTTPClient) {
				rErr := &http.Response{
					Status:     http.StatusText(http.StatusInternalServerError),
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}

				mock.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
					time.Sleep(500 * time.Millisecond)
					return rErr, nil
				})
			},
			ctxTimeout: 100 * time.Millisecond,
			wantErr:    true,
		},
		{
			name:             "request body error",
			requestBodyError: true,
			setupMocks: func(mock *MockHTTPClient) {
				rErr := &http.Response{
					Status:     http.StatusText(http.StatusInternalServerError),
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewReader([]byte{})),
				}
				mock.EXPECT().Do(gomock.Any()).Return(rErr, nil)
			},
			wantErr: true,
		},
		{
			name: "close error",
			setupMocks: func(mock *MockHTTPClient) {
				rErr := &http.Response{
					Status:     http.StatusText(http.StatusInternalServerError),
					StatusCode: http.StatusInternalServerError,
					Body:       testutil.NewErrorCloser("close error"),
				}
				mock.EXPECT().Do(gomock.Any()).Return(rErr, nil)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockHTTP := NewMockHTTPClient(ctrl)
			if tt.setupMocks != nil {
				tt.setupMocks(mockHTTP)
			}

			ctx := t.Context()

			if tt.ctxTimeout > 0 {
				timeoutCtx, cancel := context.WithTimeout(t.Context(), tt.ctxTimeout)

				defer cancel()

				ctx = timeoutCtx
			}

			r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", tt.body)
			require.NoError(t, err)

			if tt.requestBodyError {
				r.GetBody = func() (io.ReadCloser, error) {
					return nil, errors.New("ERROR")
				}
			}

			opts := []Option{
				WithRetryIfFn(RetryIfForReadRequests),
				WithAttempts(4),
				WithDelay(100 * time.Millisecond),
				WithDelayFactor(1.2),
				WithJitter(50 * time.Millisecond),
			}

			retrier, err := New(mockHTTP, opts...)
			require.NoError(t, err)

			resp, err := retrier.Do(r)
			if resp != nil {
				_ = resp.Body.Close()
			}

			require.Equal(t, tt.wantErr, err != nil, "Do() error = %v, wantErr %v", err, tt.wantErr)

			if tt.wantErr {
				require.Nil(t, resp, "Do() must return a nil response on every error path")

				return
			}

			if tt.wantStatus != 0 {
				require.NotNil(t, resp, "Do() response should not be nil")
				require.Equal(t, tt.wantStatus, resp.StatusCode, "Do() status = %v, wantStatus %v", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// flakyClient is a thread-safe [HTTPClient] used by the concurrency test. It
// fails the first call of each goroutine with a transient error, then succeeds.
type flakyClient struct {
	mu       sync.Mutex
	failNext map[*http.Request]bool
}

func newFlakyClient() *flakyClient {
	return &flakyClient{failNext: make(map[*http.Request]bool)}
}

func (f *flakyClient) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.failNext[req] {
		f.failNext[req] = true

		return nil, errors.New("transient network error")
	}

	return &http.Response{
		Status:     http.StatusText(http.StatusOK),
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}, nil
}

// TestHTTPRetrier_Do_concurrent verifies that a single retrier instance shared
// across goroutines handles overlapping Do calls without data races.
func TestHTTPRetrier_Do_concurrent(t *testing.T) {
	t.Parallel()

	retrier, err := New(
		newFlakyClient(),
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(3),
		WithDelay(1*time.Millisecond),
		WithDelayFactor(2),
		WithJitter(1*time.Millisecond),
	)
	require.NoError(t, err)

	const goroutines = 16

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()

			r, rerr := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", bytes.NewReader([]byte(`body`)))
			assert.NoError(t, rerr)

			resp, derr := retrier.Do(r)
			assert.NoError(t, derr)

			if resp != nil {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.NoError(t, resp.Body.Close())
			}
		}()
	}

	wg.Wait()
}

// TestHTTPRetrier_Do_bodyReplayOnRetry verifies the next-attempt body is opened
// lazily from GetBody and reused across retries.
func TestHTTPRetrier_Do_bodyReplayOnRetry(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	rErr := &http.Response{
		Status:     http.StatusText(http.StatusInternalServerError),
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}
	rOK := &http.Response{
		Status:     http.StatusText(http.StatusOK),
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}

	mockHTTP.EXPECT().Do(gomock.Any()).Return(rErr, nil)
	mockHTTP.EXPECT().Do(gomock.Any()).Return(rOK, nil)

	var getBodyCalls int

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", bytes.NewReader([]byte(`payload`)))
	require.NoError(t, err)

	origGetBody := r.GetBody
	r.GetBody = func() (io.ReadCloser, error) {
		getBodyCalls++

		return origGetBody()
	}

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(3),
		WithDelay(1*time.Millisecond),
		WithJitter(1*time.Millisecond),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, getBodyCalls, "GetBody should be opened exactly once, only for the retry")
}

// TestHTTPRetrier_Do_bodyNotReplayable verifies that when a retry is required
// but the request body has already been consumed and GetBody is nil, Do stops
// retrying and returns the documented error instead of resending a consumed
// body.
func TestHTTPRetrier_Do_bodyNotReplayable(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	rErr := &http.Response{
		Status:     http.StatusText(http.StatusServiceUnavailable),
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}

	// Only one attempt must be performed: the retry is aborted because the
	// consumed body cannot be recreated.
	mockHTTP.EXPECT().Do(gomock.Any()).Return(rErr, nil)

	// A streaming (non-replayable) body: net/http does not auto-set GetBody
	// for reader types it does not recognize.
	body := struct{ io.Reader }{bytes.NewReader([]byte(`streaming payload`))}

	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/", body)
	require.NoError(t, err)
	require.Nil(t, r.GetBody)

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForWriteRequests),
		WithAttempts(3),
		WithDelay(1*time.Millisecond),
		WithJitter(1*time.Millisecond),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r) //nolint:bodyclose // resp is nil on this error path; nothing to close.
	require.Nil(t, resp, "the retriable response body has been closed, so no response is returned")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrBodyNotReplayable)
}

// TestHTTPRetrier_Do_contextCancelStopsTimer verifies that when the request
// context is canceled mid-flight, Do returns an error and the per-call timer is
// stopped (no leak, no panic).
func TestHTTPRetrier_Do_contextCancelStopsTimer(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	mockHTTP.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
		time.Sleep(200 * time.Millisecond)

		return &http.Response{
			Status:     http.StatusText(http.StatusInternalServerError),
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}, nil
	}).AnyTimes()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(5),
		WithDelay(10*time.Millisecond),
		WithJitter(1*time.Millisecond),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r)
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.Nil(t, resp, "a canceled request must return a nil response, not a stale/closed one")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestHTTPRetrier_Do_respectsRetryAfter verifies that, when enabled, the chosen
// retry delay honors the server's Retry-After header over the (tiny) schedule.
// The onRetry hook captures the delay so the test need not wait it out.
func TestHTTPRetrier_Do_respectsRetryAfter(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	h := http.Header{}
	h.Set("Retry-After", "1") // 1s, far larger than the 1ms schedule

	resp503 := &http.Response{
		Status:     http.StatusText(http.StatusServiceUnavailable),
		StatusCode: http.StatusServiceUnavailable,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}
	// Only one call: the context cancels during the 1s Retry-After wait.
	mockHTTP.EXPECT().Do(gomock.Any()).Return(resp503, nil)

	var gotDelay time.Duration

	onRetry := func(_ uint, delay time.Duration, _ *http.Response, _ error) {
		gotDelay = delay
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)

	const jitter = 50 * time.Millisecond

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(3),
		WithDelay(1*time.Millisecond),
		WithJitter(jitter),
		WithRespectRetryAfter(),
		WithOnRetry(onRetry),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r)
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err) // context canceled during the 1s Retry-After wait
	// The honored Retry-After (1s) overrides the ~1ms schedule, and jitter is added
	// on top so clients that got the same value do not re-synchronize.
	require.GreaterOrEqual(t, gotDelay, time.Second, "must wait at least the Retry-After")
	require.Less(t, gotDelay, time.Second+jitter, "jitter must be added on top of Retry-After")
}

// trackingBody is an io.ReadCloser that records whether Close was called.
type trackingBody struct {
	io.Reader

	closed *bool
}

func (b trackingBody) Close() error {
	*b.closed = true

	return nil
}

// TestHTTPRetrier_Do_dropsResponseWhenClientReturnsBoth verifies Do upholds the
// response-XOR-error contract (and closes the stray body) when a non-conforming
// client returns a response alongside an error.
func TestHTTPRetrier_Do_dropsResponseWhenClientReturnsBoth(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	closed := false
	resp := &http.Response{
		Status:     http.StatusText(http.StatusInternalServerError),
		StatusCode: http.StatusInternalServerError,
		Body:       trackingBody{bytes.NewReader(nil), &closed},
	}
	mockHTTP.EXPECT().Do(gomock.Any()).Return(resp, errors.New("boom")) // non-conforming: both non-nil

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	require.NoError(t, err)

	// retryIfFn returns false -> stop on the first attempt while an error is present.
	retrier, err := New(mockHTTP, WithRetryIfFn(func(_ *http.Response, _ error) bool { return false }))
	require.NoError(t, err)

	gotResp, gotErr := retrier.Do(r) //nolint:bodyclose // gotResp is nil; the stray body is closed by Do.
	require.Nil(t, gotResp, "Do must not return a response alongside an error")
	require.Error(t, gotErr)
	require.True(t, closed, "the stray response body must be closed")
}

// TestHTTPRetrier_Do_noBodyLeakOnCancel verifies the replay body is opened lazily:
// a retry aborted by cancellation must not open (and thus cannot leak) a body.
func TestHTTPRetrier_Do_noBodyLeakOnCancel(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	ctx, cancel := context.WithCancel(t.Context())

	mockHTTP.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
		cancel() // context ends during the (retryable) attempt

		return &http.Response{
			Status:     http.StatusText(http.StatusServiceUnavailable),
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}, nil
	})

	var getBodyCalls int

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader([]byte(`payload`)))
	require.NoError(t, err)

	origGetBody := r.GetBody
	r.GetBody = func() (io.ReadCloser, error) {
		getBodyCalls++

		return origGetBody()
	}

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForWriteRequests),
		WithAttempts(3),
		WithDelay(time.Millisecond),
		WithJitter(time.Nanosecond),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r) //nolint:bodyclose // resp is nil on the cancellation path.
	require.Nil(t, resp)
	require.Error(t, err)
	require.Zero(t, getBodyCalls, "the replay body must not be opened for a retry aborted by cancellation")
}

// TestHTTPRetrier_Do_onRetryNotCalledOnCancel verifies onRetry does not fire for
// a retry preempted by context cancellation during the attempt.
func TestHTTPRetrier_Do_onRetryNotCalledOnCancel(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	ctx, cancel := context.WithCancel(t.Context())

	mockHTTP.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
		cancel() // context ends during the attempt

		return &http.Response{
			Status:     http.StatusText(http.StatusInternalServerError),
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}, nil
	})

	var onRetryCalls int

	onRetry := func(_ uint, _ time.Duration, _ *http.Response, _ error) { onRetryCalls++ }

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(3),
		WithDelay(time.Millisecond),
		WithJitter(time.Nanosecond),
		WithOnRetry(onRetry),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r) //nolint:bodyclose // resp is nil on the cancellation path.
	require.Nil(t, resp)
	require.Error(t, err)
	require.Zero(t, onRetryCalls, "onRetry must not fire for a retry preempted by cancellation")
}

// TestHTTPRetrier_Do_onRetry verifies the observability callback fires once per
// scheduled retry with increasing 1-based attempt numbers.
func TestHTTPRetrier_Do_onRetry(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	mockHTTP.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			Status:     http.StatusText(http.StatusInternalServerError),
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}, nil
	}).Times(3)

	var attempts []uint

	onRetry := func(attempt uint, _ time.Duration, _ *http.Response, _ error) {
		attempts = append(attempts, attempt)
	}

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	require.NoError(t, err)

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(3),
		WithDelay(1*time.Millisecond),
		WithJitter(1*time.Nanosecond),
		WithOnRetry(onRetry),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r)
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.NoError(t, err)
	require.Equal(t, []uint{1, 2}, attempts) // 3 attempts -> 2 scheduled retries
}

// TestHTTPRetrier_Do_preCanceledContext verifies Do fails fast without any HTTP
// call when the request context is already done.
func TestHTTPRetrier_Do_preCanceledContext(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl) // no Do call expected

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)

	retrier, err := New(mockHTTP, WithRetryIfFn(RetryIfForReadRequests))
	require.NoError(t, err)

	resp, err := retrier.Do(r) //nolint:bodyclose // resp is nil on the pre-cancel path; nothing to close.
	require.Nil(t, resp)
	require.Error(t, err)
	require.ErrorContains(t, err, "before first attempt")
}

// TestHTTPRetrier_Do_maxDelayCaps verifies WithMaxDelay is wired into the
// backoff so the gap between attempts stops growing once the cap is reached.
func TestHTTPRetrier_Do_maxDelayCaps(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHTTP := NewMockHTTPClient(ctrl)

	var timestamps []time.Time

	newErr := func() *http.Response {
		return &http.Response{
			Status:     http.StatusText(http.StatusServiceUnavailable),
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}
	}

	mockHTTP.EXPECT().Do(gomock.Any()).DoAndReturn(func(_ *http.Request) (*http.Response, error) {
		timestamps = append(timestamps, time.Now())

		return newErr(), nil
	}).Times(4)

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	require.NoError(t, err)

	retrier, err := New(
		mockHTTP,
		WithRetryIfFn(RetryIfForReadRequests),
		WithAttempts(4),
		WithDelay(10*time.Millisecond),
		WithDelayFactor(8),            // uncapped 3rd gap would be ~640ms
		WithJitter(1*time.Nanosecond), // 1ns ceiling -> jitter is always 0
		WithMaxDelay(30*time.Millisecond),
	)
	require.NoError(t, err)

	resp, err := retrier.Do(r)
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.NoError(t, err)
	require.Len(t, timestamps, 4, "all attempts should run")

	// Without the cap the third gap would be ~640ms; capped it sits near 30ms.
	gap3 := timestamps[3].Sub(timestamps[2])
	require.Greater(t, gap3, 15*time.Millisecond, "capped gap should still be near the cap")
	require.Less(t, gap3, 120*time.Millisecond, "gap must be capped well below the raw exponential value")
}
