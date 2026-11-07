package srvlifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var errClosed = errors.New("server closed")

// fakeServer serves until Stop is called, then returns errClosed.
type fakeServer struct {
	stopOnce  sync.Once
	stopped   chan struct{}
	stopCalls atomic.Int32
	stopErr   error
	serveErr  error // when set, Serve returns it immediately
}

func newFakeServer() *fakeServer {
	return &fakeServer{stopped: make(chan struct{})}
}

func (f *fakeServer) Serve(_ net.Listener) error {
	if f.serveErr != nil {
		return f.serveErr
	}

	<-f.stopped

	return errClosed
}

func (f *fakeServer) Shutdown(_ context.Context) error {
	f.stopCalls.Add(1)
	f.stopOnce.Do(func() { close(f.stopped) })

	return f.stopErr
}

var _ Server = (*fakeServer)(nil)

// nilServer is a fakeServer whose Serve returns nil at once.
type nilServer struct {
	*fakeServer
}

func (nilServer) Serve(net.Listener) error { return nil }

// testListener records Close calls.
type testListener struct {
	net.Listener

	closeErr error
	closed   atomic.Int32
}

func (l *testListener) Close() error {
	l.closed.Add(1)
	return l.closeErr
}

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

// testSetup is a Lifecycle with its fake server and observable dependencies.
type testSetup struct {
	lc       *Lifecycle
	srv      *fakeServer
	listener *testListener
	wg       *sync.WaitGroup
	signal   chan struct{}
	logs     *syncBuffer
}

func newTestSetup(srv *fakeServer) *testSetup {
	ts := &testSetup{
		srv:      srv,
		listener: &testListener{},
		wg:       &sync.WaitGroup{},
		signal:   make(chan struct{}),
		logs:     &syncBuffer{},
	}

	lc, err := New(srv, ts.listener,
		WithName("test"),
		WithLogger(slog.New(slog.NewTextHandler(ts.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))),
		WithShutdownTimeout(time.Second),
		WithShutdownWaitGroup(ts.wg),
		WithShutdownSignalChan(ts.signal),
		WithServerClosedFunc(func(err error) bool { return errors.Is(err, errClosed) }),
	)
	if err != nil {
		panic(err)
	}

	ts.lc = lc

	return ts
}

// waitClosed fails the test when ch is not closed in time.
func waitClosed(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		require.Fail(t, msg)
	}
}

// waitGroup fails the test when wg does not complete in time.
func waitGroup(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	waitClosed(t, done, "timed out waiting for the wait group")
}

func TestStart_once(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())

	require.False(t, ts.lc.Started())

	ts.lc.Start(t.Context())
	ts.lc.Start(t.Context())

	require.True(t, ts.lc.Started())
	require.Contains(t, ts.logs.String(), "listening for test requests")
	require.Contains(t, ts.logs.String(), "start ignored")

	require.NoError(t, ts.lc.Shutdown(t.Context()))
	waitGroup(t, ts.wg)
	require.Zero(t, ts.listener.closed.Load(), "Serve owns the listener of a started server")
	require.Eventually(t, func() bool {
		return strings.Contains(ts.logs.String(), "closed test server")
	}, 5*time.Second, time.Millisecond, "Serve returns after the shutdown")
}

func TestShutdown_idempotent(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())

	ts.lc.Start(t.Context())

	var wg sync.WaitGroup

	for range 4 {
		wg.Go(func() { _ = ts.lc.Shutdown(t.Context()) })
	}

	wg.Wait()

	waitGroup(t, ts.wg) // a second decrement would panic with a negative counter
	waitClosed(t, ts.lc.Done(), "ShutdownDone not closed")
	require.Equal(t, int32(4), ts.srv.stopCalls.Load(), "every call reaches Stop")
}

func TestShutdown_beforeStart(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())
	ts.listener.closeErr = errors.New("close failed")

	require.NoError(t, ts.lc.Shutdown(t.Context()))
	require.NoError(t, ts.lc.Shutdown(t.Context()))

	require.Equal(t, int32(1), ts.listener.closed.Load(), "the listener is closed once")
	require.Contains(t, ts.logs.String(), "failed closing the test server listener")

	ts.lc.Start(t.Context())

	require.False(t, ts.lc.Started(), "a shut down server is not started")
	waitGroup(t, ts.wg)
}

//nolint:paralleltest // counts the goroutines of the process
func TestShutdown_releasesGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()

	ts := newTestSetup(newFakeServer())

	ts.lc.Start(t.Context())
	require.NoError(t, ts.lc.Shutdown(t.Context()))

	// Polled on this goroutine: require.Eventually runs the condition in a
	// goroutine of its own.
	deadline := time.Now().Add(5 * time.Second)

	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			require.Fail(t, "the monitor and serve goroutines did not exit after a direct Shutdown")
		}

		time.Sleep(time.Millisecond)
	}
}

func TestDone_afterRelease(t *testing.T) {
	t.Parallel()

	started := newTestSetup(newFakeServer())
	started.lc.Start(t.Context())

	notStarted := newTestSetup(newFakeServer())

	go func() { _ = started.lc.Shutdown(context.Background()) }()
	go func() { _ = notStarted.lc.Shutdown(context.Background()) }()

	waitClosed(t, started.lc.Done(), "Done not closed")
	waitClosed(t, notStarted.lc.Done(), "Done not closed")

	released := make(chan struct{})

	go func() {
		started.wg.Wait()
		close(released)
	}()

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the wait group is released before Done is closed")
	}

	require.Equal(t, int32(1), notStarted.listener.closed.Load(), "the listener is closed before Done is closed")
}

func TestShutdown_stopError(t *testing.T) {
	t.Parallel()

	srv := newFakeServer()
	srv.stopErr = errors.New("stop failed")

	ts := newTestSetup(srv)

	ts.lc.Start(t.Context())

	require.ErrorIs(t, ts.lc.Shutdown(t.Context()), srv.stopErr)
	require.Contains(t, ts.logs.String(), "test server shutdown completed with error")
	waitGroup(t, ts.wg)
}

func TestStart_signal(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())

	ts.lc.Start(t.Context())
	close(ts.signal)

	waitClosed(t, ts.lc.Done(), "the signal did not trigger the shutdown")
	waitGroup(t, ts.wg)
	require.Contains(t, ts.logs.String(), "shutdown notification received")
}

func TestStart_contextCanceled(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())

	ctx, cancel := context.WithCancel(t.Context())

	ts.lc.Start(ctx)
	cancel()

	waitClosed(t, ts.lc.Done(), "the context cancelation did not trigger the shutdown")
	waitGroup(t, ts.wg)
	require.Contains(t, ts.logs.String(), "context canceled")
}

func TestServe_nilError(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())
	ts.lc.srv = nilServer{ts.srv}

	ts.lc.Start(t.Context())

	waitClosed(t, ts.lc.Done(), "a nil Serve result did not trigger the shutdown")
	waitGroup(t, ts.wg)
	require.Contains(t, ts.logs.String(), "test server stopped without a shutdown request")
	require.Equal(t, int32(1), ts.srv.stopCalls.Load())

	select {
	case err := <-ts.lc.ServeError():
		require.Fail(t, "a nil Serve result is not published", err)
	default:
	}
}

func TestServe_closedWithoutShutdown(t *testing.T) {
	t.Parallel()

	srv := newFakeServer()
	srv.serveErr = errClosed

	ts := newTestSetup(srv)

	ts.lc.Start(t.Context())

	waitClosed(t, ts.lc.Done(), "a self-stopped server did not trigger the shutdown")
	waitGroup(t, ts.wg)
	require.Contains(t, ts.logs.String(), "test server stopped without a shutdown request")

	select {
	case err := <-ts.lc.ServeError():
		require.Fail(t, "a normal Serve result is not published", err)
	default:
	}
}

func TestServe_closedByShutdown(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())

	ts.lc.Start(t.Context())
	require.NoError(t, ts.lc.Shutdown(t.Context()))

	require.Eventually(t, func() bool {
		return strings.Contains(ts.logs.String(), "closed test server")
	}, 5*time.Second, time.Millisecond)

	require.NotContains(t, ts.logs.String(), "stopped without a shutdown request")
}

func TestServe_failure(t *testing.T) {
	t.Parallel()

	srv := newFakeServer()
	srv.serveErr = errors.New("accept failed")

	ts := newTestSetup(srv)

	ts.lc.Start(t.Context())

	select {
	case err := <-ts.lc.ServeError():
		require.ErrorIs(t, err, srv.serveErr)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the serve failure was not published")
	}

	waitClosed(t, ts.lc.Done(), "the serve failure did not trigger the shutdown")
	waitGroup(t, ts.wg)
	require.Contains(t, ts.logs.String(), "unexpected test server failure")
}

func TestServe_failureChannelFull(t *testing.T) {
	t.Parallel()

	srv := newFakeServer()
	srv.serveErr = errors.New("second failure")

	ts := newTestSetup(srv)

	// Pre-fill the buffer so the non-blocking publish drops the new failure
	// instead of blocking or overwriting the first one.
	firstErr := errors.New("first failure")
	ts.lc.serveErr <- firstErr

	ts.lc.started = true
	ts.wg.Add(1)
	ts.lc.serve()

	select {
	case err := <-ts.lc.ServeError():
		require.ErrorIs(t, err, firstErr, "the first published failure must be preserved")
	default:
		require.Fail(t, "expected the first serve failure to remain published")
	}

	select {
	case err := <-ts.lc.ServeError():
		require.Fail(t, "the second serve failure must be dropped", err)
	default:
	}

	waitGroup(t, ts.wg)
}

func TestNew(t *testing.T) {
	t.Parallel()

	ls := &testListener{}

	_, err := New(nil, ls)
	require.ErrorIs(t, err, ErrNilServer)

	_, err = New(newFakeServer(), nil)
	require.ErrorIs(t, err, ErrNilListener)

	_, err = New(newFakeServer(), ls, nil)
	require.Error(t, err)

	_, err = New(newFakeServer(), ls, WithName(""))
	require.Error(t, err)

	lc, err := New(newFakeServer(), ls)
	require.NoError(t, err)
	require.Equal(t, "network", lc.cfg.name)
	require.Equal(t, 30*time.Second, lc.cfg.shutdownTimeout)
	require.NotNil(t, lc.cfg.logger)
	require.NotNil(t, lc.cfg.shutdownWaitGroup)
	require.NotNil(t, lc.cfg.shutdownSignalChan)
	require.True(t, lc.cfg.serverClosedFn(http.ErrServerClosed))
	require.False(t, lc.cfg.serverClosedFn(errClosed))
}

func TestAddr(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	ls, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	l, err := New(newFakeServer(), ls)
	require.NoError(t, err)
	require.Equal(t, ls.Addr(), l.Addr())
	require.NoError(t, l.Shutdown(t.Context()))
}

func TestStart_nilContext(t *testing.T) {
	t.Parallel()

	ts := newTestSetup(newFakeServer())

	//nolint:staticcheck // a nil ctx is replaced with context.Background()
	ts.lc.Start(nil)

	require.True(t, ts.lc.Started())
	require.NoError(t, ts.lc.Shutdown(t.Context()))
	waitGroup(t, ts.wg)
}
