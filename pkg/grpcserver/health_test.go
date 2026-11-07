package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// toggleChecker fails while fail is set.
type toggleChecker struct {
	fail atomic.Bool
}

func (c *toggleChecker) HealthCheck(_ context.Context) error {
	if c.fail.Load() {
		return errors.New("down")
	}

	return nil
}

// blockingChecker signals started, then blocks until its context is done.
type blockingChecker struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingChecker() *blockingChecker {
	return &blockingChecker{started: make(chan struct{})}
}

func (c *blockingChecker) HealthCheck(ctx context.Context) error {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()

	return ctx.Err() //nolint:wrapcheck
}

// waitStarted waits for c to start running.
func (c *blockingChecker) waitStarted(t *testing.T) {
	t.Helper()

	select {
	case <-c.started:
	case <-time.After(10 * time.Second):
		require.Fail(t, "the check did not start")
	}
}

func okCheck(id string) healthrun.HealthCheck {
	return healthrun.New(id, healthrun.HealthCheckFunc(func(_ context.Context) error { return nil }))
}

func failCheck(id string) healthrun.HealthCheck {
	return healthrun.New(id, healthrun.HealthCheckFunc(func(_ context.Context) error { return errors.New("down") }))
}

// newHealthTestServer starts a server with the health service and returns it
// with a connected health client.
func newHealthTestServer(t *testing.T, opts ...Option) (*GRPCServer, healthpb.HealthClient) {
	t.Helper()

	base := []Option{WithLogger(discardLogger()), WithServerAddr("127.0.0.1:0")}

	srv, err := New(t.Context(), NopBinder(), append(base, opts...)...)
	require.NoError(t, err)

	srv.StartServer()

	t.Cleanup(func() { _ = srv.Shutdown(context.WithoutCancel(t.Context())) })

	conn, err := grpc.NewClient(srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return srv, healthpb.NewHealthClient(conn)
}

// recvStatus receives the next Watch status.
func recvStatus(t *testing.T, stream grpc.ServerStreamingClient[healthpb.HealthCheckResponse]) healthpb.HealthCheckResponse_ServingStatus {
	t.Helper()

	resp, err := stream.Recv()
	require.NoError(t, err)

	return resp.GetStatus()
}

func TestWithHealthService(t *testing.T) {
	t.Parallel()

	checks := []healthrun.HealthCheck{okCheck("a")}

	cfg := defaultConfig()
	require.NoError(t, WithHealthService(checks)(cfg))
	require.Len(t, cfg.health.checks, 1)
	require.Equal(t, "a", cfg.health.checks[0].ID)
	require.Equal(t, defaultHealthWatchInterval, cfg.health.watchInterval)
	require.Zero(t, cfg.health.timeout)

	require.NoError(t, WithHealthService(nil,
		WithHealthCheckTimeout(time.Second),
		WithHealthWatchInterval(time.Minute),
	)(cfg))
	require.Empty(t, cfg.health.checks, "the last WithHealthService wins")
	require.Equal(t, time.Second, cfg.health.timeout)
	require.Equal(t, time.Minute, cfg.health.watchInterval)

	tests := []struct {
		name string
		opt  HealthOption
	}{
		{name: "nil option", opt: nil},
		{name: "zero timeout", opt: WithHealthCheckTimeout(0)},
		{name: "negative timeout", opt: WithHealthCheckTimeout(-time.Second)},
		{name: "zero interval", opt: WithHealthWatchInterval(0)},
		{name: "negative interval", opt: WithHealthWatchInterval(-time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Error(t, WithHealthService(nil, tt.opt)(defaultConfig()))
		})
	}
}

func TestHealthService_checksCopied(t *testing.T) {
	t.Parallel()

	checks := []healthrun.HealthCheck{okCheck("a")}

	_, client := newHealthTestServer(t, WithHealthService(checks))

	checks[0] = failCheck("a")

	resp, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: "a"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus(), "the checks slice must be copied")
}

func TestHealthService_registration(t *testing.T) {
	t.Parallel()

	s, _ := newHealthTestServer(t, WithHealthService(nil))
	require.Contains(t, s.GetServiceInfo(), healthpb.Health_ServiceDesc.ServiceName)

	s, _ = newTestServer(t, &testService{})
	require.NotContains(t, s.GetServiceInfo(), healthpb.Health_ServiceDesc.ServiceName)
	require.Nil(t, s.health)

	binder := funcBinder(func(_ context.Context, r grpc.ServiceRegistrar) error {
		healthpb.RegisterHealthServer(r, health.NewServer())
		return nil
	})

	_, err := New(t.Context(), binder,
		WithServerAddr("127.0.0.1:0"),
		WithLogger(discardLogger()),
		WithHealthService(nil),
	)
	require.ErrorIs(t, err, ErrServiceRegistration)
}

func TestHealthService_Check(t *testing.T) {
	t.Parallel()

	_, client := newHealthTestServer(t, WithHealthService([]healthrun.HealthCheck{
		okCheck("db"),
		failCheck("cache"),
		okCheck("dup"),
		failCheck("dup"),
		okCheck(""),
	}))

	tests := []struct {
		service string
		want    healthpb.HealthCheckResponse_ServingStatus
	}{
		{service: "", want: healthpb.HealthCheckResponse_NOT_SERVING},
		{service: "db", want: healthpb.HealthCheckResponse_SERVING},
		{service: "cache", want: healthpb.HealthCheckResponse_NOT_SERVING},
		{service: "dup", want: healthpb.HealthCheckResponse_NOT_SERVING},
	}

	for _, tt := range tests {
		resp, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: tt.service})
		require.NoError(t, err, "service %q", tt.service)
		require.Equal(t, tt.want, resp.GetStatus(), "service %q", tt.service)
	}

	_, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: "unknown"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestHealthService_CheckOverall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		checks []healthrun.HealthCheck
		want   healthpb.HealthCheckResponse_ServingStatus
	}{
		{name: "no checks", want: healthpb.HealthCheckResponse_SERVING},
		{name: "all pass", checks: []healthrun.HealthCheck{okCheck("a"), okCheck("b")}, want: healthpb.HealthCheckResponse_SERVING},
		{name: "empty id fails", checks: []healthrun.HealthCheck{okCheck("a"), failCheck("")}, want: healthpb.HealthCheckResponse_NOT_SERVING},
		{name: "nil checker", checks: []healthrun.HealthCheck{healthrun.New("a", nil)}, want: healthpb.HealthCheckResponse_NOT_SERVING},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, client := newHealthTestServer(t, WithHealthService(tt.checks))

			resp, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{})
			require.NoError(t, err)
			require.Equal(t, tt.want, resp.GetStatus())
		})
	}
}

func TestHealthService_CheckRunsOnlyServiceChecks(t *testing.T) {
	t.Parallel()

	var other atomic.Int32

	_, client := newHealthTestServer(t, WithHealthService([]healthrun.HealthCheck{
		okCheck("db"),
		healthrun.New("other", healthrun.HealthCheckFunc(func(_ context.Context) error {
			other.Add(1)
			return nil
		})),
	}))

	resp, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: "db"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
	require.Zero(t, other.Load())
}

func TestHealthService_CheckTimeout(t *testing.T) {
	t.Parallel()

	_, client := newHealthTestServer(t, WithHealthService(
		[]healthrun.HealthCheck{healthrun.New("slow", newBlockingChecker())},
		WithHealthCheckTimeout(20*time.Millisecond),
	))

	resp, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: "slow"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, resp.GetStatus())
}

func TestHealthService_CheckAfterShutdownStarts(t *testing.T) {
	t.Parallel()

	s, client := newHealthTestServer(t, WithHealthService([]healthrun.HealthCheck{okCheck("db")}))

	s.health.shutdown()

	for _, svc := range []string{"", "db"} {
		resp, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: svc})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, resp.GetStatus(), "service %q", svc)
	}

	_, err := client.Check(rpcContext(t), &healthpb.HealthCheckRequest{Service: "unknown"})
	require.Equal(t, codes.NotFound, status.Code(err))

	list, err := client.List(rpcContext(t), &healthpb.HealthListRequest{})
	require.NoError(t, err)
	require.Len(t, list.GetStatuses(), 2)

	for svc, st := range list.GetStatuses() {
		require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, st.GetStatus(), "service %q", svc)
	}
}

func TestHealthService_shutdownDuringRun(t *testing.T) {
	t.Parallel()

	slow := newBlockingChecker()

	h := newHealthServer(&healthConfig{
		checks:        []healthrun.HealthCheck{okCheck("db"), healthrun.New("slow", slow)},
		watchInterval: time.Minute,
	}, discardLogger())

	type result struct {
		check *healthpb.HealthCheckResponse
		list  *healthpb.HealthListResponse
	}

	done := make(chan result)

	go func() {
		var r result

		r.check, _ = h.Check(t.Context(), &healthpb.HealthCheckRequest{Service: "db"})
		r.list, _ = h.List(t.Context(), &healthpb.HealthListRequest{})

		done <- r
	}()

	slow.waitStarted(t)
	h.shutdown()

	select {
	case r := <-done:
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, r.check.GetStatus(), "the check completed before shutdown")
		require.Len(t, r.list.GetStatuses(), 3)

		for svc, st := range r.list.GetStatuses() {
			require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, st.GetStatus(), "service %q", svc)
		}
	case <-time.After(10 * time.Second):
		require.Fail(t, "shutdown did not interrupt the running checks")
	}

	resp, err := h.Check(t.Context(), &healthpb.HealthCheckRequest{Service: "slow"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, resp.GetStatus())
}

func TestHealthService_List(t *testing.T) {
	t.Parallel()

	_, client := newHealthTestServer(t, WithHealthService([]healthrun.HealthCheck{
		okCheck("db"),
		failCheck("cache"),
		okCheck("dup"),
		okCheck("dup"),
		failCheck(""),
	}))

	resp, err := client.List(rpcContext(t), &healthpb.HealthListRequest{})
	require.NoError(t, err)

	got := make(map[string]healthpb.HealthCheckResponse_ServingStatus)
	for svc, st := range resp.GetStatuses() {
		got[svc] = st.GetStatus()
	}

	require.Equal(t, map[string]healthpb.HealthCheckResponse_ServingStatus{
		"":      healthpb.HealthCheckResponse_NOT_SERVING,
		"db":    healthpb.HealthCheckResponse_SERVING,
		"cache": healthpb.HealthCheckResponse_NOT_SERVING,
		"dup":   healthpb.HealthCheckResponse_SERVING,
	}, got)
}

func TestHealthService_ListOverallServing(t *testing.T) {
	t.Parallel()

	_, client := newHealthTestServer(t, WithHealthService([]healthrun.HealthCheck{okCheck("db")}))

	resp, err := client.List(rpcContext(t), &healthpb.HealthListRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatuses()[""].GetStatus())
}

func TestWithHealthService_tooManyServices(t *testing.T) {
	t.Parallel()

	checks := make([]healthrun.HealthCheck, maxHealthListServices)
	for i := range checks {
		checks[i] = okCheck(fmt.Sprintf("c%03d", i))
	}

	require.Error(t, WithHealthService(checks)(defaultConfig()))

	fit := append(slices.Clone(checks[1:]), okCheck("c001"), okCheck(""))
	require.NoError(t, WithHealthService(fit)(defaultConfig()), "duplicate and empty IDs add no service")

	_, client := newHealthTestServer(t, WithHealthService(fit))

	resp, err := client.List(rpcContext(t), &healthpb.HealthListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetStatuses(), maxHealthListServices)
}

func TestNewHealthServer_logging(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelDebug)

	h := newHealthServer(&healthConfig{
		checks: []healthrun.HealthCheck{
			okCheck("db"),
			okCheck("db"),
			healthrun.New("nochk", nil),
			healthrun.New("panicky", healthrun.HealthCheckFunc(func(_ context.Context) error { panic("boom") })),
		},
		watchInterval: time.Hour,
	}, logger)

	require.Len(t, sink.entries(t, "healthcheck registered with a duplicate ID"), 1)
	require.Len(t, sink.entries(t, "healthcheck registered with a nil checker"), 1)

	resp, err := h.Check(t.Context(), &healthpb.HealthCheckRequest{Service: "panicky"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, resp.GetStatus())
	require.Len(t, sink.entries(t, "healthcheck checker panicked"), 1, "service handlers log errors")
}

func TestHealthService_publish(t *testing.T) {
	t.Parallel()

	ch := make(chan healthpb.HealthCheckResponse_ServingStatus, 1)
	svc := &healthService{subs: map[chan healthpb.HealthCheckResponse_ServingStatus]struct{}{ch: {}}}

	svc.publish(t.Context(), healthpb.HealthCheckResponse_SERVING)
	svc.publish(t.Context(), healthpb.HealthCheckResponse_NOT_SERVING)

	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, <-ch, "an unread status is replaced")
	require.Empty(t, ch)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	svc.publish(ctx, healthpb.HealthCheckResponse_SERVING)
	require.Empty(t, ch, "a stopped poller publishes nothing")
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, svc.last)
}

func TestHealthService_watchUpdatesAfterShutdown(t *testing.T) {
	t.Parallel()

	h := newHealthServer(&healthConfig{watchInterval: time.Hour}, discardLogger())

	updates := make(chan healthpb.HealthCheckResponse_ServingStatus, 1)
	updates <- healthpb.HealthCheckResponse_SERVING

	h.shutdown()

	stream := &watchStream{ctx: t.Context(), sent: make(chan *healthpb.HealthCheckResponse, 16)}
	err := h.watchUpdates(t.Context(), &watchSender{stream: stream}, updates)

	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, (<-stream.sent).GetStatus(), "an update queued before shutdown is not sent")
	require.Empty(t, stream.sent)
}

func TestHealthService_WatchSharesPoller(t *testing.T) {
	t.Parallel()

	var dbRuns, allRuns atomic.Int32

	h := newHealthServer(&healthConfig{
		checks: []healthrun.HealthCheck{
			healthrun.New("db", healthrun.HealthCheckFunc(func(_ context.Context) error {
				dbRuns.Add(1)
				return nil
			})),
			healthrun.New("other", healthrun.HealthCheckFunc(func(_ context.Context) error {
				allRuns.Add(1)
				return nil
			})),
		},
		watchInterval: time.Hour,
	}, discardLogger())

	type watch struct {
		cancel context.CancelFunc
		stream *watchStream
		done   chan error
	}

	open := func(service string) watch {
		ctx, cancel := context.WithCancel(t.Context())
		w := watch{
			cancel: cancel,
			stream: &watchStream{ctx: ctx, sent: make(chan *healthpb.HealthCheckResponse, 16)},
			done:   make(chan error, 1),
		}

		go func() { w.done <- h.Watch(&healthpb.HealthCheckRequest{Service: service}, w.stream) }()

		require.Equal(t, healthpb.HealthCheckResponse_SERVING, (<-w.stream.sent).GetStatus())

		return w
	}

	closeWatch := func(w watch) {
		w.cancel()
		require.Equal(t, codes.Canceled, status.Code(<-w.done))
	}

	first := open("db")
	second := open("db")

	require.Equal(t, int32(1), dbRuns.Load(), "the streams of a service share one run")
	require.Zero(t, allRuns.Load(), "a service runs only its checks")

	overall := open("")

	require.Equal(t, int32(2), dbRuns.Load())
	require.Equal(t, int32(1), allRuns.Load())

	closeWatch(first)
	closeWatch(second)
	closeWatch(overall)

	for id, svc := range h.services {
		require.Eventually(t, func() bool {
			svc.mu.Lock()
			defer svc.mu.Unlock()

			return svc.cancel == nil && !svc.hasLast
		}, 10*time.Second, time.Millisecond, "service %q: the poller stops with its last stream", id)
	}

	require.Never(t, func() bool { return dbRuns.Load() > 2 }, 50*time.Millisecond, 5*time.Millisecond,
		"closed streams must not run the checks again")

	closeWatch(open("db"))
	require.Equal(t, int32(3), dbRuns.Load(), "a new poller runs the checks again")
}

func TestHealthService_Watch(t *testing.T) {
	t.Parallel()

	db := &toggleChecker{}

	_, client := newHealthTestServer(t, WithHealthService(
		[]healthrun.HealthCheck{healthrun.New("db", db)},
		WithHealthWatchInterval(5*time.Millisecond),
	))

	ctx, cancel := context.WithCancel(rpcContext(t))

	stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{Service: "db"})
	require.NoError(t, err)

	require.Equal(t, healthpb.HealthCheckResponse_SERVING, recvStatus(t, stream))

	db.fail.Store(true)
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, recvStatus(t, stream), "a change is sent")

	db.fail.Store(false)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, recvStatus(t, stream), "a change is sent")

	cancel()

	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))
}

func TestHealthService_WatchSendsOnlyChanges(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32

	h := newHealthServer(&healthConfig{
		checks: []healthrun.HealthCheck{healthrun.New("db", healthrun.HealthCheckFunc(func(_ context.Context) error {
			runs.Add(1)
			return nil
		}))},
		watchInterval: time.Millisecond,
	}, discardLogger())

	stream := &watchStream{ctx: t.Context(), sent: make(chan *healthpb.HealthCheckResponse, 16)}
	done := make(chan error)

	go func() { done <- h.Watch(&healthpb.HealthCheckRequest{Service: "db"}, stream) }()

	require.Equal(t, healthpb.HealthCheckResponse_SERVING, (<-stream.sent).GetStatus())
	require.Eventually(t, func() bool { return runs.Load() >= 5 }, 10*time.Second, time.Millisecond)

	h.shutdown()

	require.Equal(t, codes.Unavailable, status.Code(<-done))
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, (<-stream.sent).GetStatus())
	require.Empty(t, stream.sent, "repeated SERVING results must not be sent")
}

func TestHealthService_WatchUnknown(t *testing.T) {
	t.Parallel()

	_, client := newHealthTestServer(t, WithHealthService(nil))

	ctx, cancel := context.WithCancel(rpcContext(t))

	stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{Service: "unknown"})
	require.NoError(t, err)

	require.Equal(t, healthpb.HealthCheckResponse_SERVICE_UNKNOWN, recvStatus(t, stream))

	cancel()

	_, err = stream.Recv()
	require.Equal(t, codes.Canceled, status.Code(err))
}

func TestHealthService_WatchEndsOnShutdown(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	s, client := newHealthTestServer(t,
		WithShutdownWaitGroup(wg),
		WithHealthService([]healthrun.HealthCheck{okCheck("db")}, WithHealthWatchInterval(time.Hour)),
	)

	known, err := client.Watch(rpcContext(t), &healthpb.HealthCheckRequest{Service: "db"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, recvStatus(t, known))

	unknown, err := client.Watch(rpcContext(t), &healthpb.HealthCheckRequest{Service: "unknown"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVICE_UNKNOWN, recvStatus(t, unknown))

	shutdownCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	require.NoError(t, s.Shutdown(shutdownCtx), "open Watch streams must not block the graceful stop")
	waitTimeout(t, wg)

	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, recvStatus(t, known))

	_, err = known.Recv()
	require.Equal(t, codes.Unavailable, status.Code(err))

	_, err = unknown.Recv()
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestHealthService_WatchShutdownDuringRun(t *testing.T) {
	t.Parallel()

	slow := newBlockingChecker()

	h := newHealthServer(&healthConfig{
		checks:        []healthrun.HealthCheck{healthrun.New("slow", slow)},
		watchInterval: time.Hour,
	}, discardLogger())

	stream := &watchStream{ctx: t.Context(), sent: make(chan *healthpb.HealthCheckResponse, 16)}
	done := make(chan error)

	go func() { done <- h.Watch(&healthpb.HealthCheckRequest{}, stream) }()

	slow.waitStarted(t)
	h.shutdown()

	require.Equal(t, codes.Unavailable, status.Code(<-done))
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, (<-stream.sent).GetStatus())
	require.Empty(t, stream.sent)
}

func TestHealthService_WatchSendError(t *testing.T) {
	t.Parallel()

	h := newHealthServer(&healthConfig{
		checks:        []healthrun.HealthCheck{okCheck("db")},
		watchInterval: time.Hour,
	}, discardLogger())

	sendErr := errors.New("send failed")

	for _, svc := range []string{"db", "unknown"} {
		stream := &watchStream{ctx: t.Context(), err: sendErr}
		require.ErrorIs(t, h.Watch(&healthpb.HealthCheckRequest{Service: svc}, stream), sendErr, "service %q", svc)
	}
}

func TestHealthService_WatchDeadline(t *testing.T) {
	t.Parallel()

	slow := newBlockingChecker()

	h := newHealthServer(&healthConfig{
		checks:        []healthrun.HealthCheck{healthrun.New("slow", slow)},
		watchInterval: time.Hour,
	}, discardLogger())

	ctx, cancel := context.WithCancel(t.Context())

	stream := &watchStream{ctx: ctx, sent: make(chan *healthpb.HealthCheckResponse, 16)}
	done := make(chan error)

	go func() { done <- h.Watch(&healthpb.HealthCheckRequest{}, stream) }()

	slow.waitStarted(t)
	cancel()

	require.Equal(t, codes.Canceled, status.Code(<-done))
	require.Empty(t, stream.sent, "the result of a canceled run must not be sent")
}

// watchStream is an in-memory Watch server stream.
type watchStream struct {
	grpc.ServerStream

	ctx  context.Context //nolint:containedctx
	sent chan *healthpb.HealthCheckResponse
	err  error
}

func (s *watchStream) Context() context.Context {
	return s.ctx
}

func (s *watchStream) Send(resp *healthpb.HealthCheckResponse) error {
	if s.err != nil {
		return s.err
	}

	s.sent <- resp

	return nil
}

// gateChecker counts its runs and blocks each run until release is closed or
// its context is done.
type gateChecker struct {
	runs     atomic.Int32
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func newGateChecker() *gateChecker {
	return &gateChecker{
		started:  make(chan struct{}, 16),
		release:  make(chan struct{}),
		canceled: make(chan struct{}, 16),
	}
}

func (c *gateChecker) HealthCheck(ctx context.Context) error {
	c.runs.Add(1)

	c.started <- struct{}{}

	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		c.canceled <- struct{}{}
		return ctx.Err() //nolint:wrapcheck
	}
}

// waitWaiters waits until the run in progress of svc has n waiters.
func waitWaiters(t *testing.T, svc *healthService, n int) {
	t.Helper()

	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()

		return svc.call != nil && svc.call.waiters == n
	}, 10*time.Second, time.Millisecond)
}

func TestHealthService_sharedRun(t *testing.T) {
	t.Parallel()

	gate := newGateChecker()

	h := newHealthServer(&healthConfig{
		checks:        []healthrun.HealthCheck{healthrun.New("db", gate)},
		watchInterval: time.Hour,
	}, discardLogger())

	type result struct {
		check *healthpb.HealthCheckResponse
		list  *healthpb.HealthListResponse
		err   error
	}

	leaveCtx, leave := context.WithCancel(t.Context())
	leaving := make(chan result, 1)
	results := make(chan result, 2)

	go func() {
		r, err := h.Check(leaveCtx, &healthpb.HealthCheckRequest{})
		leaving <- result{check: r, err: err}
	}()

	go func() {
		r, err := h.Check(t.Context(), &healthpb.HealthCheckRequest{})
		results <- result{check: r, err: err}
	}()

	go func() {
		r, err := h.List(t.Context(), &healthpb.HealthListRequest{})
		results <- result{list: r, err: err}
	}()

	waitWaiters(t, h.services[""], 3)

	leave()

	r := <-leaving
	require.Equal(t, codes.Canceled, status.Code(r.err))
	waitWaiters(t, h.services[""], 2)
	require.Empty(t, gate.canceled, "the run continues while callers wait for it")

	close(gate.release)

	for range 2 {
		r := <-results
		require.NoError(t, r.err)

		if r.check != nil {
			require.Equal(t, healthpb.HealthCheckResponse_SERVING, r.check.GetStatus())
		} else {
			require.Equal(t, healthpb.HealthCheckResponse_SERVING, r.list.GetStatuses()[""].GetStatus())
		}
	}

	require.Equal(t, int32(1), gate.runs.Load(), "concurrent callers share one run")

	resp, err := h.Check(t.Context(), &healthpb.HealthCheckRequest{Service: "db"})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
	require.Equal(t, int32(2), gate.runs.Load(), "a later call starts a new run")
}

func TestHealthService_sharedRunCanceled(t *testing.T) {
	t.Parallel()

	gate := newGateChecker()

	h := newHealthServer(&healthConfig{
		checks:        []healthrun.HealthCheck{healthrun.New("db", gate)},
		watchInterval: time.Hour,
	}, discardLogger())

	for _, call := range []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := h.Check(ctx, &healthpb.HealthCheckRequest{Service: "db"})
			return err
		},
		func(ctx context.Context) error {
			_, err := h.List(ctx, &healthpb.HealthListRequest{})
			return err
		},
	} {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)

		go func() { done <- call(ctx) }()

		<-gate.started
		cancel()

		require.Equal(t, codes.Canceled, status.Code(<-done))

		select {
		case <-gate.canceled:
		case <-time.After(10 * time.Second):
			require.Fail(t, "the run was not canceled when its last caller left")
		}
	}

	for _, svc := range h.services {
		svc.mu.Lock()
		require.Nil(t, svc.call)
		svc.mu.Unlock()
	}
}
