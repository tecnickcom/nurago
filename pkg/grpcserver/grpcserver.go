/*
Package grpcserver provides a configurable gRPC server bootstrap for Go
services.

The package assembles the server from:
  - option-driven configuration ([New] + functional [Option]s)
  - pluggable service registration via [Binder]
  - a default interceptor chain for trace IDs, request logging, panic recovery,
    and default deadlines
  - graceful shutdown orchestration via context and/or signal channel

The lifecycle contract matches the httpserver package, so gRPC and HTTP servers
can share the same wait group and shutdown signal.

# Default Interceptor Chain

Every unary and streaming RPC passes through, outermost first:
  - Request inject: reads the trace ID from incoming metadata (default key
    "x-request-id", see [WithTraceIDMetadataKey]), validates it, and generates
    a UUIDv7 when it is missing or invalid. The ID is stored in the context
    with traceid.ForceContext and returned to the client in the response
    header metadata. Values of the context passed to [New] are exposed only for
    the keys set with [WithContextValues].
  - Request logger: one log entry per RPC with the trace ID, method, peer,
    user agent, response code, and duration. At debug level, unary request and
    response messages are dumped as JSON, redacted, and capped (see
    [WithMaxDumpSize]); streams log message counts. Disable it for all methods
    with [WithoutRequestLogger] or for specific methods with
    [WithoutMethodLogger].
  - Panic recovery: a panic in a handler or inner interceptor is logged with a
    stack trace and answered with codes.Internal (see [WithPanicHandlerFunc]).
    A panic raised by the request inject or logger step, for example by a
    [RedactFn] or a log handler, is recovered too: before the handler runs it
    is answered the same way, and after the handler runs it is logged and the
    handler result is returned.
  - Default deadline: a unary RPC that arrives without a deadline gets the one
    set by [WithRequestTimeout]. Streams are exempt unless a per-method timeout
    is set with [WithMethodTimeout], which is applied in the transport before
    the interceptors run.
  - Custom interceptors added with [WithUnaryInterceptor] and
    [WithStreamInterceptor], in the order given.

The interceptors of the instrumenters added with [WithInstrumenter] run
between the request logger and the panic recovery. When instrumenters are
set, a second panic recovery runs between the request logger and their
interceptors, so a panic they raise is answered and logged like a handler
panic.

# Instrumentation

[WithInstrumenter] adds the server options of a
[github.com/tecnickcom/nurago/pkg/grpcutil.ServerInstrumenter], such as the
Prometheus metrics of the metrics/prometheus/prometheusgrpc package or the
OpenTelemetry spans and metrics of the metrics/opentel/opentelgrpc package.

# Health Service

[WithHealthService] registers the standard grpc.health.v1.Health service,
backed by github.com/tecnickcom/nurago/pkg/healthcheck/healthrun checks
(healthcheck.HealthCheck is the same type):
  - Check: runs the checks of the requested service. The service name ""
    covers every check; each non-empty check ID is also a service name.
    Unknown services are answered with codes.NotFound.
  - List: runs every check once and reports all the services, at most 100,
    since [WithHealthService] accepts at most 99 distinct check IDs.
  - Watch: while a service has open streams, runs its checks once per
    interval (see [WithHealthWatchInterval]), shared by all the streams, and
    sends the status when it changes.
  - Shutdown: once [GRPCServer.Shutdown] starts, every service reports
    NOT_SERVING, and open Watch streams receive NOT_SERVING and end with
    codes.Unavailable, so they do not delay the graceful stop.

Checks run on every Check and List request and once per Watch interval of
each watched service. Concurrent Check, List and Watch calls on the same
service share one run. Bound them with [WithHealthCheckTimeout], and exclude
frequently polled health methods from request logging with
[WithoutMethodLogger] when needed.

# Behavior

  - Lifecycle: non-blocking start, context-aware shutdown, configurable
    shutdown timeout, external wait-group and signal integration,
    abnormal-termination reporting via [GRPCServer.ServeError], and ephemeral
    port discovery via [GRPCServer.Addr].
  - Shutdown: [GRPCServer.Shutdown] stops accepting connections and waits for
    in-flight RPCs (GracefulStop). When its context expires first, open
    connections are closed (Stop) and the context error is returned.
  - Startup validation: invalid options, duplicate service registrations, and
    handlers that do not implement their service interface are reported by
    [New] as wrapped sentinel errors, instead of terminating the process as
    grpc.Server.RegisterService does.
  - Transport: plaintext HTTP/2 by default, or TLS from cert/key material
    ([WithTLSCertData]) or a custom [WithTLSConfig]. Message and header size
    limits and keepalive parameters are configurable.

# Security

Server reflection ([WithReflection]) lets any client list and describe every
registered service and message type. Enable it only on internal or
administrative listeners that are not reachable from the public internet, or
protect it with an authentication interceptor.

The health service List method returns every check ID, which can name
internal dependencies. The health service is not exempt from the custom
interceptors: an authentication interceptor must allow Check and Watch
explicitly when load balancers or probes call them without credentials, and
keep List authenticated.

The server listens in plaintext unless TLS is configured. Use TLS, or run
behind a TLS-terminating proxy, for any listener reachable from an untrusted
network.

# When To Use

  - A service exposes gRPC APIs and needs sane defaults for logging, trace
    IDs, panics, and deadlines.
  - Shutdown must drain in-flight RPCs before the process exits.
  - The gRPC server must follow the same lifecycle as the service's HTTP
    servers.
*/
package grpcserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"sync"

	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Binder registers the gRPC services on the server.
type Binder interface {
	// BindGRPC registers the services on s, typically by calling the generated
	// Register<Service>Server functions. s accepts registrations only until
	// BindGRPC returns: later ones are logged and ignored.
	BindGRPC(ctx context.Context, s grpc.ServiceRegistrar) error
}

// NopBinder returns a binder that registers no services.
func NopBinder() Binder {
	return &nopBinder{}
}

// nopBinder is a no-operation binder implementation.
type nopBinder struct{}

// BindGRPC implements the Binder interface.
func (b *nopBinder) BindGRPC(_ context.Context, _ grpc.ServiceRegistrar) error { return nil }

// GRPCServer defines the gRPC server object.
type GRPCServer struct {
	cfg        *config
	ctx        context.Context //nolint:containedctx
	grpcServer *grpc.Server
	health     *healthServer // nil unless WithHealthService is set
	lc         *srvlifecycle.Lifecycle
}

// New constructs a gRPC server with the default interceptor chain, binds the
// listener, and registers the services supplied by binder.
//
// The listener is bound immediately: on success the server already holds the
// network address. If the returned server is never started, call
// [GRPCServer.Shutdown] to release the bound listener. A nil ctx is treated as
// context.Background().
//
// RPC contexts are not derived from ctx: canceling ctx triggers a graceful
// shutdown, and in-flight RPCs are allowed to complete within the shutdown
// grace period. RPC contexts expose only the values of ctx stored under the
// keys set with [WithContextValues].
//
//nolint:contextcheck // a nil ctx is deliberately replaced with context.Background()
func New(ctx context.Context, binder Binder, opts ...Option) (*GRPCServer, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if binder == nil {
		return nil, ErrNilBinder
	}

	cfg := defaultConfig()

	for _, applyOpt := range opts {
		err := applyOpt(cfg)
		if err != nil {
			return nil, err
		}
	}

	cfg.baseValues = contextValues(ctx, cfg.contextKeys)

	err := validateTLSConfig(cfg.tlsConfig)
	if err != nil {
		return nil, err
	}

	srv, err := newServer(cfg.serverOptions())
	if err != nil {
		return nil, err
	}

	listener, err := netListener(ctx, cfg.serverAddr)
	if err != nil {
		srv.Stop()

		return nil, err
	}

	cfg.logger = cfg.logger.With(
		slog.String("component", "grpcserver"),
		slog.String("addr", listener.Addr().String()),
	)

	s := &GRPCServer{
		cfg:        cfg,
		ctx:        ctx,
		grpcServer: srv,
	}

	s.lc, err = srvlifecycle.New(lifecycleServer{s: s}, listener, cfg.lifecycleOptions()...)
	if err != nil {
		srv.Stop()

		_ = listener.Close()

		return nil, fmt.Errorf("failed creating the grpc server lifecycle: %w", err)
	}

	s.health = optionalHealthServer(cfg.health, cfg.logger)

	err = registerServices(ctx, binder, srv, s.health, cfg.reflection, cfg.logger)
	if err != nil {
		// Stops the never-started server and closes the listener.
		_ = s.lc.Shutdown(ctx)

		return nil, err
	}

	initializeServer(srv, cfg.instrumenters)

	return s, nil
}

// Addr returns the network address the server listener is bound to. It is
// primarily useful when binding to an ephemeral port (":0") to discover the
// actual port assigned by the operating system.
func (s *GRPCServer) Addr() net.Addr {
	return s.lc.Addr()
}

// ServeError returns a channel that receives the error that caused the server
// to stop serving unexpectedly (any Serve failure other than a normal shutdown).
// The channel is buffered (size 1) and never receives a nil value. A server that
// stops through Shutdown leaves it empty.
func (s *GRPCServer) ServeError() <-chan error {
	return s.lc.ServeError()
}

// GetServiceInfo returns the registered services, keyed by fully qualified
// service name. It wraps grpc.Server.GetServiceInfo.
func (s *GRPCServer) GetServiceInfo() map[string]grpc.ServiceInfo {
	return s.grpcServer.GetServiceInfo()
}

// StartServerCtx starts the server in a background goroutine with
// context-aware shutdown support.
//
// The provided ctx controls shutdown only: its cancelation triggers the
// graceful-shutdown sequence. RPC contexts expose the values selected with
// WithContextValues from the ctx passed to New, not from this one.
//
// It is a no-op if the server has already been started or has already been shut
// down: this keeps the shutdown wait group balanced (a single Add(1) matched by a
// single decrement) and avoids serving twice on the same listener. A nil ctx is
// treated as context.Background().
func (s *GRPCServer) StartServerCtx(ctx context.Context) {
	s.lc.Start(ctx)
}

// StartServer starts the server in background using the context from New().
func (s *GRPCServer) StartServer() {
	s.StartServerCtx(s.ctx)
}

// Shutdown gracefully shuts down the server.
//
// It stops accepting new connections and waits for in-flight RPCs to complete.
// When ctx expires first, all open connections are closed and ctx.Err() is
// returned; handlers that ignore the cancelation of their RPC context may keep
// running after Shutdown returns.
//
// It is safe to call Shutdown any number of times and from multiple paths
// (e.g. manually and via the internal context/signal goroutine): the wait group
// is decremented exactly once, and only when the server was actually started.
func (s *GRPCServer) Shutdown(ctx context.Context) error {
	return s.lc.Shutdown(ctx) //nolint:wrapcheck // stop already wraps its error
}

// stop makes the health service report NOT_SERVING, then runs GracefulStop
// and falls back to Stop when ctx expires first. A server that was never
// started has no connections to drain and is stopped directly.
func (s *GRPCServer) stop(ctx context.Context) error {
	if s.health != nil {
		s.health.shutdown()
	}

	if !s.lc.Started() {
		s.grpcServer.Stop()

		return nil
	}

	done := make(chan struct{})

	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()

	// Stop closes every connection and aborts the pending GracefulStop.
	// It does not wait for running handlers.
	return waitStop(ctx, done, s.grpcServer.Stop)
}

// lifecycleServer adapts a GRPCServer to srvlifecycle.Server.
type lifecycleServer struct {
	s *GRPCServer
}

// Serve serves gRPC requests on l.
func (a lifecycleServer) Serve(l net.Listener) error {
	return a.s.grpcServer.Serve(l) //nolint:wrapcheck // returned as-is to the lifecycle
}

// Shutdown stops the server.
func (a lifecycleServer) Shutdown(ctx context.Context) error {
	return a.s.stop(ctx)
}

// waitStop waits for done and returns nil. When ctx expires first and done is
// still open, it calls forceStop and returns the context error.
func waitStop(ctx context.Context, done <-chan struct{}, forceStop func()) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}

	select {
	case <-done:
		return nil
	default:
	}

	forceStop()

	return fmt.Errorf("grpc server graceful shutdown interrupted: %w", ctx.Err())
}

// newServer calls grpc.NewServer and returns the panic it raises for invalid
// options as an error.
func newServer(opts []grpc.ServerOption) (*grpc.Server, error) {
	var srv *grpc.Server

	p := catchPanic(func() { srv = grpc.NewServer(opts...) })
	if p != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidServerOption, p)
	}

	return srv, nil
}

// validateTLSConfig rejects a TLS configuration that carries no certificate
// material, which would otherwise fail every handshake at runtime.
func validateTLSConfig(tlsConfig *tls.Config) error {
	if tlsConfig != nil && len(tlsConfig.Certificates) == 0 &&
		tlsConfig.GetCertificate == nil && tlsConfig.GetConfigForClient == nil {
		return fmt.Errorf("failed configuring the grpc server: %w", ErrInvalidTLSConfig)
	}

	return nil
}

// netListener creates a TCP listener for the given server address. TLS is
// handled by the gRPC transport credentials, not by the listener.
func netListener(ctx context.Context, serverAddr string) (net.Listener, error) {
	var lc net.ListenConfig

	ls, err := lc.Listen(ctx, "tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed creating the grpc server address listener: %w", err)
	}

	return ls, nil
}

// registerServices runs the binder against a validating registrar and, when
// enabled, registers the health and reflection services. The registrar is
// closed before it returns.
func registerServices(
	ctx context.Context,
	binder Binder,
	srv *grpc.Server,
	health *healthServer,
	withReflection bool,
	logger *slog.Logger,
) error {
	reg := newRegistrar(srv, logger)
	defer reg.close()

	err := binder.BindGRPC(ctx, reg)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrServiceRegistration, err)
	}

	if health != nil {
		healthpb.RegisterHealthServer(reg, health)
	}

	if withReflection {
		reflection.Register(reg)
	}

	return reg.result()
}

// initializeServer calls InitializeServer on the instrumenters that
// implement grpcutil.ServerInitializer.
func initializeServer(srv *grpc.Server, instrumenters []grpcutil.ServerInstrumenter) {
	for _, in := range instrumenters {
		if si, ok := in.(grpcutil.ServerInitializer); ok {
			si.InitializeServer(srv)
		}
	}
}

// registrar validates each registration before delegating to grpc.Server,
// which terminates the process on a duplicate service name, a handler that
// does not implement the service interface, or a registration after Serve.
// The first failure is recorded and later registrations are ignored.
// Registrations after close are logged and ignored.
type registrar struct {
	mu     sync.Mutex
	srv    *grpc.Server
	logger *slog.Logger
	names  map[string]struct{}
	err    error
	closed bool
}

// newRegistrar returns an open registrar for srv.
func newRegistrar(srv *grpc.Server, logger *slog.Logger) *registrar {
	return &registrar{
		srv:    srv,
		logger: logger,
		names:  make(map[string]struct{}),
	}
}

// RegisterService implements grpc.ServiceRegistrar.
func (r *registrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		name := ""
		if desc != nil {
			name = desc.ServiceName
		}

		r.logger.Error("service registration ignored: registrar closed", slog.String("grpc_service", name))

		return
	}

	if r.err != nil {
		return
	}

	r.err = validateService(r.names, desc, impl)
	if r.err != nil {
		return
	}

	r.names[desc.ServiceName] = struct{}{}
	r.srv.RegisterService(desc, impl)
}

// GetServiceInfo returns the services registered so far. It lets
// reflection.Register and other helpers that inspect the registrar work as
// they do with a *grpc.Server.
func (r *registrar) GetServiceInfo() map[string]grpc.ServiceInfo {
	return r.srv.GetServiceInfo()
}

// result returns the first registration failure, or nil.
func (r *registrar) result() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.err
}

// close makes the registrar ignore later registrations.
func (r *registrar) close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
}

// validateService replicates the grpc.Server.RegisterService checks that would
// otherwise terminate the process. registered holds the service names
// already registered.
func validateService(registered map[string]struct{}, desc *grpc.ServiceDesc, impl any) error {
	if desc == nil {
		return fmt.Errorf("%w: nil service descriptor", ErrServiceRegistration)
	}

	if _, dup := registered[desc.ServiceName]; dup {
		return fmt.Errorf("%w: duplicate service %q", ErrServiceRegistration, desc.ServiceName)
	}

	if impl == nil {
		return nil
	}

	ht := reflect.TypeOf(desc.HandlerType)
	if ht == nil || ht.Kind() != reflect.Pointer || ht.Elem().Kind() != reflect.Interface {
		return fmt.Errorf("%w: service %q has an invalid HandlerType", ErrServiceRegistration, desc.ServiceName)
	}

	if !reflect.TypeOf(impl).Implements(ht.Elem()) {
		return fmt.Errorf("%w: handler of type %T does not implement %v", ErrServiceRegistration, impl, ht.Elem())
	}

	return nil
}
