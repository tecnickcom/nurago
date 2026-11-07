package healthrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// syncBuffer is a bytes.Buffer safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func newTestLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}

	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	checks := []HealthCheck{New("a", &testChecker{}), New("b", &testChecker{})}

	r := NewRunner(checks, WithLogger(discardLogger()), WithTimeout(time.Second))
	require.Len(t, r.checks, 2)
	require.Equal(t, time.Second, r.timeout)

	checks[0] = New("mutated", &testChecker{})
	require.Equal(t, "a", r.checks[0].ID, "the checks slice must be copied")

	require.NotNil(t, NewRunner(nil, WithLogger(nil)).logger)
}

func TestNewRunner_WarnsInvalidChecks(t *testing.T) {
	t.Parallel()

	logger, buf := newTestLogger()

	NewRunner([]HealthCheck{
		New("dup", &testChecker{}),
		New("dup", &testChecker{}),
		New("", &testChecker{}),
		New("nochk", nil),
	}, WithLogger(logger))

	out := buf.String()
	require.Contains(t, out, "duplicate ID")
	require.Contains(t, out, "empty ID")
	require.Contains(t, out, "nil checker")
}

func TestRunner_Run(t *testing.T) {
	t.Parallel()

	errA := errors.New("first")
	errB := errors.New("second")

	logger, buf := newTestLogger()

	r := NewRunner([]HealthCheck{
		New("ok", &testChecker{err: nil}),
		New("fail", &testChecker{err: errA}),
		New("dup", &testChecker{err: errA}),
		New("dup", &testChecker{err: nil}),
		New("dup", &testChecker{err: errB}),
		New("dup_ok", &testChecker{err: nil}),
		New("dup_ok", &testChecker{err: nil}),
		New("nochk", nil),
		New("panicky", &panicChecker{msg: "checker exploded"}),
	}, WithLogger(logger))

	got := r.Run(t.Context())

	require.Len(t, got, 6)
	require.NoError(t, got["ok"])
	require.ErrorIs(t, got["fail"], errA)
	require.ErrorIs(t, got["dup"], errB, "the last failure wins")
	require.NoError(t, got["dup_ok"])
	require.ErrorIs(t, got["nochk"], ErrNoChecker)
	require.ErrorIs(t, got["panicky"], ErrCheckPanic)

	logged := buf.String()
	require.Contains(t, logged, "checker panicked")
	require.Contains(t, logged, "checker exploded")
	require.Contains(t, logged, "stack")
	require.Equal(t, 1, strings.Count(logged, "panicked"), "a nil checker is not a panic")
}

func TestRunner_Run_NoChecks(t *testing.T) {
	t.Parallel()

	require.Empty(t, NewRunner(nil).Run(t.Context()))
}

func TestRunner_Run_Timeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	r := NewRunner([]HealthCheck{
		New("fast", &testChecker{err: nil}),
		New("slow", &releaseChecker{release: release}),
	}, WithTimeout(250*time.Millisecond))

	got := r.Run(t.Context())

	require.NoError(t, got["fast"])
	require.ErrorIs(t, got["slow"], ErrCheckTimeout)
}

func TestRunner_Run_ContextCanceled(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	r := NewRunner([]HealthCheck{New("slow", &releaseChecker{release: release})})

	require.ErrorIs(t, r.Run(ctx)["slow"], ErrCheckTimeout)
}

func TestRunner_Run_Concurrent(t *testing.T) {
	t.Parallel()

	const n = 8

	var started sync.WaitGroup

	started.Add(n)

	release := make(chan struct{})
	checks := make([]HealthCheck, n)

	for i := range checks {
		checks[i] = New(fmt.Sprintf("c%02d", i), HealthCheckFunc(func(_ context.Context) error {
			started.Done()
			<-release

			return nil
		}))
	}

	// Every check must start before any is released, which only happens when
	// they run in parallel.
	go func() {
		started.Wait()
		close(release)
	}()

	got := NewRunner(checks).Run(t.Context())

	require.Len(t, got, n)

	for id, err := range got {
		require.NoError(t, err, id)
	}
}

func TestDrainResults(t *testing.T) {
	t.Parallel()

	results := []checkResult{
		{id: "a", err: ErrCheckTimeout},
		{id: "b", err: ErrCheckTimeout},
	}

	resCh := make(chan indexedResult, len(results))
	for _, r := range []indexedResult{{index: 0, err: nil}, {index: 1, err: errors.New("boom")}} {
		resCh <- r
	}

	drainResults(results, resCh)

	require.NoError(t, results[0].err)
	require.EqualError(t, results[1].err, "boom")
}
