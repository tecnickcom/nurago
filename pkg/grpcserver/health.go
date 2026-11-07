package grpcserver

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	// defaultHealthWatchInterval is the default interval between the check
	// runs of a watched service.
	defaultHealthWatchInterval = 5 * time.Second

	// maxHealthListServices is the maximum number of services, including the
	// overall "" one, that WithHealthService accepts. It matches the List limit
	// of the grpc-go health server.
	maxHealthListServices = 100
)

// errHealthShutdown reports that the health server has started shutting down.
var errHealthShutdown = errors.New("health server is shutting down")

// HealthOption configures the health service enabled with [WithHealthService].
type HealthOption func(*healthConfig) error

// healthConfig contains the health service configuration.
type healthConfig struct {
	checks        []healthrun.HealthCheck
	timeout       time.Duration
	watchInterval time.Duration
}

// WithHealthCheckTimeout bounds each run of the checks (see
// healthrun.WithTimeout). Checks that do not return in time are reported as
// failed. When not set, a run ends only when the server starts shutting down
// or when no RPC waits for it any more: until then, a check that does not
// return delays every Check, List and Watch that shares the run.
func WithHealthCheckTimeout(timeout time.Duration) HealthOption {
	return func(cfg *healthConfig) error {
		if timeout <= 0 {
			return errors.New("invalid healthCheckTimeout")
		}

		cfg.timeout = timeout

		return nil
	}
}

// WithHealthWatchInterval sets the interval between the check runs of a
// watched service (default 5 seconds). While a service has open Watch
// streams, its checks run once per interval, shared by all of them.
func WithHealthWatchInterval(interval time.Duration) HealthOption {
	return func(cfg *healthConfig) error {
		if interval <= 0 {
			return errors.New("invalid healthWatchInterval")
		}

		cfg.watchInterval = interval

		return nil
	}
}

// healthServer implements grpc.health.v1.Health on top of healthrun
// runners. The "" service runs every check; each non-empty check ID is a
// service that runs the checks registered under that ID.
type healthServer struct {
	healthpb.UnimplementedHealthServer

	services map[string]*healthService
	interval time.Duration
	stopCtx  context.Context //nolint:containedctx // canceled when the server starts shutting down
	stop     context.CancelFunc
}

// healthService is one service of the health server. Concurrent callers share
// one run of its checks. While it has Watch subscribers, a single poller runs
// its checks once per watch interval and sends each result to all of them.
type healthService struct {
	checks *healthrun.Runner

	mu      sync.Mutex
	call    *healthRun // the run in progress; nil when none runs
	subs    map[chan healthpb.HealthCheckResponse_ServingStatus]struct{}
	cancel  context.CancelFunc // stops the poller; nil when no poller runs
	last    healthpb.HealthCheckResponse_ServingStatus
	hasLast bool
}

// newHealthServer builds the health service for cfg. The check runners log
// through logger. Construction warnings are logged only by the "" runner,
// since each ID group holds a subset of its checks.
func newHealthServer(cfg *healthConfig, logger *slog.Logger) *healthServer {
	groups := map[string][]healthrun.HealthCheck{"": cfg.checks}

	for _, hc := range cfg.checks {
		if hc.ID != "" {
			groups[hc.ID] = append(groups[hc.ID], hc)
		}
	}

	groupLogger := slog.New(&minLevelHandler{Handler: logger.Handler(), level: slog.LevelError})
	services := make(map[string]*healthService, len(groups))

	for id, checks := range groups {
		l := groupLogger
		if id == "" {
			l = logger
		}

		services[id] = &healthService{
			checks: healthrun.NewRunner(checks,
				healthrun.WithLogger(l),
				healthrun.WithTimeout(cfg.timeout),
			),
			subs: make(map[chan healthpb.HealthCheckResponse_ServingStatus]struct{}),
		}
	}

	stopCtx, stop := context.WithCancel(context.Background())

	return &healthServer{
		services: services,
		interval: cfg.watchInterval,
		stopCtx:  stopCtx,
		stop:     stop,
	}
}

// healthServiceCount returns the number of services, including the overall
// "" one, that checks define.
func healthServiceCount(checks []healthrun.HealthCheck) int {
	ids := map[string]struct{}{"": {}}

	for _, hc := range checks {
		ids[hc.ID] = struct{}{}
	}

	return len(ids)
}

// minLevelHandler passes to the wrapped handler only the records at or above
// level.
type minLevelHandler struct {
	slog.Handler

	level slog.Level
}

// Enabled reports whether l is at or above the minimum level and enabled in the
// wrapped handler.
func (h *minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level && h.Handler.Enabled(ctx, l)
}

func (h *minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &minLevelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h *minLevelHandler) WithGroup(name string) slog.Handler {
	return &minLevelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}

// Check reports the status of the requested service. An unknown service is
// answered with codes.NotFound.
func (h *healthServer) Check(ctx context.Context, in *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	svc, ok := h.services[in.GetService()]
	if !ok {
		return nil, status.Error(codes.NotFound, "unknown service") //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	results, err := h.run(ctx, svc)

	switch {
	case errors.Is(err, errHealthShutdown):
		return notServing(), nil
	case err != nil:
		return nil, status.FromContextError(err).Err() //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	return &healthpb.HealthCheckResponse{Status: overallStatus(results)}, nil
}

// List reports the status of every service, including the overall "" one,
// from a single run of all the checks.
func (h *healthServer) List(ctx context.Context, _ *healthpb.HealthListRequest) (*healthpb.HealthListResponse, error) {
	results, err := h.run(ctx, h.services[""])

	switch {
	case errors.Is(err, errHealthShutdown):
		statuses := make(map[string]*healthpb.HealthCheckResponse, len(h.services))

		for id := range h.services {
			statuses[id] = notServing()
		}

		return &healthpb.HealthListResponse{Statuses: statuses}, nil
	case err != nil:
		return nil, status.FromContextError(err).Err() //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	return listResponse(results), nil
}

// listResponse builds the List response from the outcomes of all the checks.
// A check with an empty ID contributes only to the overall "" status.
func listResponse(results map[string]error) *healthpb.HealthListResponse {
	statuses := make(map[string]*healthpb.HealthCheckResponse, len(results)+1)

	for id, err := range results {
		if id != "" {
			statuses[id] = &healthpb.HealthCheckResponse{Status: servingStatus(err == nil)}
		}
	}

	statuses[""] = &healthpb.HealthCheckResponse{Status: overallStatus(results)}

	return &healthpb.HealthListResponse{Statuses: statuses}
}

// Watch sends the status of the requested service when it changes. The checks
// of a service run once per watch interval while it has open Watch streams,
// and every stream of the service receives the same results. A new stream
// first receives the latest result, when one is available. An unknown service
// is reported once as SERVICE_UNKNOWN. When the server starts shutting down, a
// known service is reported as NOT_SERVING and the stream ends with
// codes.Unavailable, so the graceful stop does not wait for it.
func (h *healthServer) Watch(
	in *healthpb.HealthCheckRequest,
	stream grpc.ServerStreamingServer[healthpb.HealthCheckResponse],
) error {
	ctx, cancel := h.runContext(stream.Context())
	defer cancel()

	w := &watchSender{stream: stream}

	svc, ok := h.services[in.GetService()]
	if !ok {
		err := w.send(healthpb.HealthCheckResponse_SERVICE_UNKNOWN)
		if err != nil {
			return err
		}

		<-ctx.Done()

		return h.watchEnd(ctx)
	}

	updates := h.subscribe(svc)
	defer svc.unsubscribe(updates)

	return h.watchUpdates(ctx, w, updates)
}

// watchUpdates sends each status received from updates through w, until ctx
// is done or shutdown is called.
func (h *healthServer) watchUpdates(
	ctx context.Context,
	w *watchSender,
	updates <-chan healthpb.HealthCheckResponse_ServingStatus,
) error {
	for {
		select {
		case st := <-updates:
			if h.shuttingDown() {
				return h.watchStop(ctx, w)
			}

			err := w.send(st)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return h.watchStop(ctx, w)
		}
	}
}

// watchStop ends a Watch stream of a known service, first sending NOT_SERVING
// through w when shutdown has been called.
func (h *healthServer) watchStop(ctx context.Context, w *watchSender) error {
	if h.shuttingDown() {
		_ = w.send(healthpb.HealthCheckResponse_NOT_SERVING)
	}

	return h.watchEnd(ctx)
}

// subscribe registers a Watch subscriber of svc and returns the channel that
// receives its statuses. The channel holds only the latest status. The first
// subscriber starts the poller; later ones first receive the latest result.
func (h *healthServer) subscribe(svc *healthService) chan healthpb.HealthCheckResponse_ServingStatus {
	ch := make(chan healthpb.HealthCheckResponse_ServingStatus, 1)

	svc.mu.Lock()
	defer svc.mu.Unlock()

	svc.subs[ch] = struct{}{}

	if svc.cancel == nil {
		ctx, cancel := context.WithCancel(h.stopCtx)
		svc.cancel = cancel

		go h.poll(ctx, svc)

		return ch
	}

	if svc.hasLast {
		ch <- svc.last
	}

	return ch
}

// unsubscribe removes the Watch subscriber ch. Removing the last subscriber
// stops the poller and discards the latest result.
func (svc *healthService) unsubscribe(ch chan healthpb.HealthCheckResponse_ServingStatus) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	delete(svc.subs, ch)

	if len(svc.subs) == 0 && svc.cancel != nil {
		svc.cancel()
		svc.cancel = nil
		svc.hasLast = false
	}
}

// publish sends st to every subscriber, replacing a status not yet received,
// unless ctx, the context of the poller that produced st, is done.
func (svc *healthService) publish(ctx context.Context, st healthpb.HealthCheckResponse_ServingStatus) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	if ctx.Err() != nil {
		return
	}

	svc.last, svc.hasLast = st, true

	for ch := range svc.subs {
		select {
		case <-ch:
		default:
		}

		ch <- st
	}
}

// poll runs the checks of svc once per watch interval and publishes each
// result, until ctx is done or shutdown is called.
func (h *healthServer) poll(ctx context.Context, svc *healthService) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		results, err := h.run(ctx, svc)
		if err != nil {
			return
		}

		svc.publish(ctx, overallStatus(results))

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// watchSender sends Watch statuses, skipping a status equal to the last one
// sent.
type watchSender struct {
	stream grpc.ServerStreamingServer[healthpb.HealthCheckResponse]
	last   healthpb.HealthCheckResponse_ServingStatus
	sent   bool
}

// send sends st unless it equals the last status sent.
func (w *watchSender) send(st healthpb.HealthCheckResponse_ServingStatus) error {
	if w.sent && st == w.last {
		return nil
	}

	err := w.stream.Send(&healthpb.HealthCheckResponse{Status: st})
	if err != nil {
		return err //nolint:wrapcheck // gRPC handlers return stream errors as-is
	}

	w.last, w.sent = st, true

	return nil
}

// shutdown marks every service as NOT_SERVING and ends the open Watch streams.
func (h *healthServer) shutdown() {
	h.stop()
}

func (h *healthServer) shuttingDown() bool {
	return h.stopCtx.Err() != nil
}

// healthRun is a run of the checks of a service, shared by its callers.
type healthRun struct {
	done    chan struct{} // closed when results is set
	results map[string]error
	waiters int
	cancel  context.CancelFunc
}

// run returns the outcomes of a run of the checks of svc. A caller joins the
// run in progress, if any, otherwise it starts one. The run is canceled when
// shutdown is called or when all of its callers have left. It returns
// errHealthShutdown once shutdown has been called, or the ctx error when ctx is
// done first.
func (h *healthServer) run(ctx context.Context, svc *healthService) (map[string]error, error) {
	if h.shuttingDown() {
		return nil, errHealthShutdown
	}

	c := h.joinRun(svc) //nolint:contextcheck // the shared run outlives any single caller and ends on shutdown or when its callers leave

	select {
	case <-c.done:
		if h.shuttingDown() {
			return nil, errHealthShutdown
		}

		return c.results, nil
	case <-ctx.Done():
		svc.leaveRun(c)

		return nil, ctx.Err() //nolint:wrapcheck // the ctx error is mapped to a gRPC status by the caller
	}
}

// joinRun returns the run in progress of svc, starting one when none runs, and
// counts the caller as one of its waiters.
func (h *healthServer) joinRun(svc *healthService) *healthRun {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	if svc.call == nil {
		ctx, cancel := context.WithCancel(h.stopCtx)
		c := &healthRun{done: make(chan struct{}), cancel: cancel}
		svc.call = c

		go func() {
			c.results = svc.checks.Run(ctx)

			cancel()
			svc.endRun(c)
			close(c.done)
		}()
	}

	svc.call.waiters++

	return svc.call
}

// leaveRun removes a waiter from c and cancels c when no waiter is left.
func (svc *healthService) leaveRun(c *healthRun) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	c.waiters--

	if c.waiters == 0 {
		c.cancel()

		if svc.call == c {
			svc.call = nil
		}
	}
}

// endRun clears c as the run in progress of svc.
func (svc *healthService) endRun(c *healthRun) {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	if svc.call == c {
		svc.call = nil
	}
}

// overallStatus returns SERVING when no check in results failed.
func overallStatus(results map[string]error) healthpb.HealthCheckResponse_ServingStatus {
	for _, err := range results {
		if err != nil {
			return healthpb.HealthCheckResponse_NOT_SERVING
		}
	}

	return healthpb.HealthCheckResponse_SERVING
}

// runContext returns a copy of ctx that is also canceled when shutdown is
// called.
func (h *healthServer) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.stopCtx, cancel)

	return ctx, func() {
		stop()
		cancel()
	}
}

// watchEnd returns the error that ends a Watch stream: codes.Unavailable on
// shutdown, otherwise the status of the ctx error.
func (h *healthServer) watchEnd(ctx context.Context) error {
	if h.shuttingDown() {
		return status.Error(codes.Unavailable, "server is shutting down") //nolint:wrapcheck // gRPC status errors are returned as-is
	}

	return status.FromContextError(ctx.Err()).Err() //nolint:wrapcheck // gRPC status errors are returned as-is
}

func servingStatus(ok bool) healthpb.HealthCheckResponse_ServingStatus {
	if ok {
		return healthpb.HealthCheckResponse_SERVING
	}

	return healthpb.HealthCheckResponse_NOT_SERVING
}

func notServing() *healthpb.HealthCheckResponse {
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}
}
