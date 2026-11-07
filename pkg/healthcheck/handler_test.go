package healthcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// barrierHealthChecker signals that it started, then blocks until released. It
// lets tests prove checks run concurrently without relying on wall-clock timing.
type barrierHealthChecker struct {
	started *sync.WaitGroup
	release <-chan struct{}
}

func (b *barrierHealthChecker) HealthCheck(_ context.Context) error {
	b.started.Done()
	<-b.release

	return nil
}

// releaseHealthChecker blocks until released, letting a test hold a check "in
// flight" deterministically without a real sleep.
type releaseHealthChecker struct {
	release <-chan struct{}
}

func (r *releaseHealthChecker) HealthCheck(_ context.Context) error {
	<-r.release

	return nil
}

// response is the fully-read outcome of a handler call, so that the response
// body is closed inside the helper and never escapes to the caller.
type response struct {
	status int
	header http.Header
	body   string
}

func serve(t *testing.T, h *Handler) response {
	t.Helper()

	rr := httptest.NewRecorder()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	require.NoError(t, err)

	h.ServeHTTP(rr, req)

	resp := rr.Result()
	defer func() {
		require.NoError(t, resp.Body.Close(), "error closing resp.Body")
	}()

	data, rerr := io.ReadAll(resp.Body)
	require.NoError(t, rerr)

	return response{status: resp.StatusCode, header: resp.Header.Clone(), body: string(data)}
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	testChecks := []HealthCheck{
		New("testcheck_1", &testHealthChecker{}),
		New("testcheck_2", &testHealthChecker{}),
	}

	// No options
	h1 := NewHandler(testChecks)
	require.Len(t, h1.Run(t.Context()), 2)
	require.Equal(t, reflect.ValueOf(httputil.NewHTTPResp(h1.logger).SendJSON).Pointer(), reflect.ValueOf(h1.writeResult).Pointer())

	// The stored slice must be a copy: mutating the caller's slice does not leak in.
	testChecks[0] = New("mutated", &testHealthChecker{})

	require.Contains(t, h1.Run(t.Context()), "testcheck_1")

	// With options
	rw := func(_ context.Context, _ http.ResponseWriter, _ int, _ any) {}
	h2 := NewHandler(testChecks, WithResultWriter(rw))
	require.Equal(t, reflect.ValueOf(rw).Pointer(), reflect.ValueOf(h2.writeResult).Pointer())

	// WithLogger must affect the default result writer (built after options).
	logger := discardLogger()
	h3 := NewHandler(testChecks, WithLogger(logger))
	require.Equal(t, logger, h3.logger)
	require.Equal(t, reflect.ValueOf(httputil.NewHTTPResp(logger).SendJSON).Pointer(), reflect.ValueOf(h3.writeResult).Pointer())
}

func TestNewHandler_WarnsInvalidIDs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	NewHandler([]HealthCheck{
		New("dup", &testHealthChecker{}),
		New("dup", &testHealthChecker{}),
		New("", &testHealthChecker{}),
		New("nochk", nil),
	}, WithLogger(logger))

	out := buf.String()
	require.Contains(t, out, "duplicate ID")
	require.Contains(t, out, "empty ID")
	require.Contains(t, out, "nil checker")
}

func TestHandler_ServeHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		checks     []HealthCheck
		opts       []HandlerOption
		wantStatus int
		wantBody   string
	}{
		{
			name: "success multiple OK",
			checks: []HealthCheck{
				New("test_01", &testHealthChecker{delay: 50 * time.Millisecond, err: nil}),
				New("test_02", &testHealthChecker{delay: 50 * time.Millisecond, err: nil}),
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"test_01":"OK","test_02":"OK"}`,
		},
		{
			name: "success multiple OK with custom response writer",
			checks: []HealthCheck{
				New("test_11", &testHealthChecker{err: nil}),
				New("test_12", &testHealthChecker{err: nil}),
			},
			opts: []HandlerOption{
				WithResultWriter(func(ctx context.Context, w http.ResponseWriter, statusCode int, data any) {
					type wrapper struct {
						Data any `json:"data"`
					}
					httputil.NewHTTPResp(discardLogger()).SendJSON(ctx, w, statusCode, &wrapper{
						Data: data,
					})
				}),
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"data":{"test_11":"OK","test_12":"OK"}}`,
		},
		{
			name: "func adapter check",
			checks: []HealthCheck{
				New("fn", HealthCheckFunc(func(_ context.Context) error { return nil })),
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"fn":"OK"}`,
		},
		{
			name: "mixed results",
			checks: []HealthCheck{
				New("test_31", &testHealthChecker{err: nil}),
				New("test_32", &testHealthChecker{err: errors.New("check error")}),
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"test_31":"OK","test_32":"check error"}`,
		},
		{
			name: "duplicate id: failure recorded before success",
			checks: []HealthCheck{
				New("dup", &testHealthChecker{err: errors.New("boom")}),
				New("dup", &testHealthChecker{err: nil}),
			},
			opts:       []HandlerOption{WithLogger(discardLogger())},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"dup":"boom"}`,
		},
		{
			name: "duplicate id: failure overrides earlier success",
			checks: []HealthCheck{
				New("dup", &testHealthChecker{err: nil}),
				New("dup", &testHealthChecker{err: errors.New("boom")}),
			},
			opts:       []HandlerOption{WithLogger(discardLogger())},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"dup":"boom"}`,
		},
		{
			name:       "no checks returns empty OK",
			checks:     nil,
			wantStatus: http.StatusOK,
			wantBody:   `{}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := serve(t, NewHandler(tt.checks, tt.opts...))

			require.Equal(t, tt.wantStatus, got.status)
			require.Equal(t, "application/json; charset=utf-8", got.header.Get("Content-Type"))
			require.Equal(t, tt.wantBody+"\n", got.body)
		})
	}
}

func TestHandler_ServeHTTP_Timeout(t *testing.T) {
	t.Parallel()

	// The slow check blocks until the test releases it, so the handler timeout is
	// what unblocks the response (no real sleep, and the goroutine cannot outlive
	// the test).
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	h := NewHandler([]HealthCheck{
		New("fast", &testHealthChecker{err: nil}),
		New("slow", &releaseHealthChecker{release: release}),
	}, WithTimeout(250*time.Millisecond))

	st := time.Now()
	got := serve(t, h)
	el := time.Since(st)

	require.Equal(t, http.StatusServiceUnavailable, got.status)
	require.Equal(t, `{"fast":"OK","slow":"`+ErrCheckTimeout.Error()+`"}`+"\n", got.body)
	// The handler must return around the timeout, well before the slow check ends.
	require.Less(t, el, 5*time.Second, "handler did not return on timeout: %s", el)
}

func TestNewHandler_NilLogger(t *testing.T) {
	t.Parallel()

	// WithLogger(nil) must fall back to slog.Default() rather than leaving a nil
	// logger, which would panic when logging invalid-ID warnings or a recovered
	// checker panic (and crash the process in the latter case).
	h := NewHandler(nil, WithLogger(nil))
	require.NotNil(t, h.logger)
}

func TestHandler_ServeHTTP_NilChecker(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := NewHandler([]HealthCheck{New("nochk", nil)}, WithLogger(logger))

	got := serve(t, h)

	require.Equal(t, http.StatusServiceUnavailable, got.status)
	require.Equal(t, `{"nochk":"`+ErrNoChecker.Error()+`"}`+"\n", got.body)
	// A nil checker must fail cleanly, not as a recovered panic with a stack dump.
	require.NotContains(t, buf.String(), "panicked")
}

func TestHandler_ServeHTTP_PanicLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := NewHandler([]HealthCheck{
		New("ok", &testHealthChecker{err: nil}),
		New("panicky", &panicHealthChecker{msg: "checker exploded"}),
	}, WithLogger(logger))

	got := serve(t, h)

	require.Equal(t, http.StatusServiceUnavailable, got.status)
	// The response exposes the sentinel, not the raw panic value.
	require.Equal(t, `{"ok":"OK","panicky":"`+ErrCheckPanic.Error()+`"}`+"\n", got.body)

	logged := buf.String()
	require.Contains(t, logged, "checker panicked")
	require.Contains(t, logged, "checker exploded")
	require.Contains(t, logged, "stack")
}

func TestHandler_ServeHTTP_Concurrent(t *testing.T) {
	t.Parallel()

	const n = 8

	var started sync.WaitGroup

	started.Add(n)

	release := make(chan struct{})

	checks := make([]HealthCheck, n)
	for i := range n {
		checks[i] = New(fmt.Sprintf("c%02d", i), &barrierHealthChecker{started: &started, release: release})
	}

	// Release the checks only once every one has started. If they ran serially
	// this would deadlock (and the test would time out) instead of completing.
	go func() {
		started.Wait()
		close(release)
	}()

	got := serve(t, NewHandler(checks, WithLogger(discardLogger())))

	require.Equal(t, http.StatusOK, got.status)

	for i := range n {
		require.Contains(t, got.body, fmt.Sprintf(`"c%02d":"OK"`, i))
	}
}

func TestHandler_Run(t *testing.T) {
	t.Parallel()

	errA := errors.New("first")
	errB := errors.New("second")

	h := NewHandler([]HealthCheck{
		New("ok", &testHealthChecker{err: nil}),
		New("fail", &testHealthChecker{err: errA}),
		New("dup", &testHealthChecker{err: errA}),
		New("dup", &testHealthChecker{err: nil}),
		New("dup", &testHealthChecker{err: errB}),
		New("dup_ok", &testHealthChecker{err: nil}),
		New("dup_ok", &testHealthChecker{err: nil}),
		New("nochk", nil),
		New("panicky", &panicHealthChecker{msg: "boom"}),
	}, WithLogger(discardLogger()))

	got := h.Run(t.Context())

	require.Len(t, got, 6)
	require.NoError(t, got["ok"])
	require.ErrorIs(t, got["fail"], errA)
	require.ErrorIs(t, got["dup"], errB, "the last failure wins")
	require.NoError(t, got["dup_ok"])
	require.ErrorIs(t, got["nochk"], ErrNoChecker)
	require.ErrorIs(t, got["panicky"], ErrCheckPanic)
}

func TestHandler_Run_NoChecks(t *testing.T) {
	t.Parallel()

	require.Empty(t, NewHandler(nil).Run(t.Context()))
}

func TestHandler_Run_Timeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	h := NewHandler([]HealthCheck{
		New("fast", &testHealthChecker{err: nil}),
		New("slow", &releaseHealthChecker{release: release}),
	}, WithTimeout(250*time.Millisecond))

	got := h.Run(t.Context())

	require.NoError(t, got["fast"])
	require.ErrorIs(t, got["slow"], ErrCheckTimeout)
}

func TestHandler_Run_ContextCanceled(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	h := NewHandler([]HealthCheck{New("slow", &releaseHealthChecker{release: release})})

	require.ErrorIs(t, h.Run(ctx)["slow"], ErrCheckTimeout)
}
