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
	"errors"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"sync"

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
	cfg          *config
	ctx          context.Context //nolint:containedctx
	grpcServer   *grpc.Server
	health       *healthServer // nil unless WithHealthService is set
	listener     net.Listener
	startedMutex sync.Mutex
	started      bool
	stopped      bool // set once Shutdown has run, guarding against a late Start.
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	monitorDone  chan struct{} // closed when the shutdown-monitor goroutine exits
	serveErr     chan error    // buffered (size 1); receives an abnormal Serve failure
}

// New constructs a gRPC server with the default interceptor chain, registers
// the services supplied by binder, and binds the listener.
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

	var health *healthServer
	if cfg.health != nil {
		health = newHealthServer(cfg.health, cfg.logger)
	}

	err = registerServices(ctx, binder, srv, health, cfg.reflection, cfg.logger)
	if err != nil {
		srv.Stop()

		_ = listener.Close()

		return nil, err
	}

	return &GRPCServer{
			cfg:          cfg,
			ctx:          ctx,
			grpcServer:   srv,
			health:       health,
			listener:     listener,
			shutdownDone: make(chan struct{}),
			monitorDone:  make(chan struct{}),
			serveErr:     make(chan error, 1),
		},
		nil
}

// Addr returns the network address the server listener is bound to. It is
// primarily useful when binding to an ephemeral port (":0") to discover the
// actual port assigned by the operating system.
func (s *GRPCServer) Addr() net.Addr {
	return s.listener.Addr()
}

// ServeError returns a channel that receives the error that caused the server
// to stop serving unexpectedly (any Serve failure other than a normal shutdown).
// The channel is buffered (size 1) and never receives a nil value. A server that
// stops through Shutdown leaves it empty.
func (s *GRPCServer) ServeError() <-chan error {
	return s.serveErr
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
//
//nolint:contextcheck // a nil ctx is deliberately replaced with context.Background()
func (s *GRPCServer) StartServerCtx(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	// Register the running server with the wait group synchronously, before any
	// goroutine (including the shutdown one) can decrement it. The matching
	// decrement happens exactly once in Shutdown via sync.Once, and only for a
	// started server.
	s.startedMutex.Lock()

	if s.started || s.stopped {
		s.startedMutex.Unlock()
		s.cfg.logger.Warn("start ignored: server already started or shut down")

		return
	}

	s.started = true
	s.cfg.shutdownWaitGroup.Add(1)
	s.startedMutex.Unlock()

	// wait for shutdown signal, direct Shutdown call, or context cancelation
	go func() { //nolint:gosec
		defer close(s.monitorDone)

		select {
		case <-s.cfg.shutdownSignalChan:
			s.cfg.logger.Debug("shutdown notification received")
		case <-s.shutdownDone:
			// Shutdown was already called directly: exit without leaking this goroutine.
			return
		case <-ctx.Done():
			s.cfg.logger.Warn("context canceled")
		}

		// The shutdown context is independent from the parent context.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.shutdownTimeout)
		defer cancel()

		_ = s.Shutdown(shutdownCtx) //nolint:contextcheck
	}()

	// start server
	go func() {
		s.serve()
	}()

	s.cfg.logger.Info("listening for grpc requests")
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
	s.cfg.logger.Debug("shutting down grpc server")

	if s.health != nil {
		s.health.shutdown()
	}

	err := s.stop(ctx)

	s.shutdownOnce.Do(func() {
		// Unblock the internal shutdown-monitor goroutine (if any),
		// so a direct Shutdown call does not leak it.
		close(s.shutdownDone)

		s.startedMutex.Lock()
		defer s.startedMutex.Unlock()

		s.stopped = true

		if s.started {
			s.cfg.shutdownWaitGroup.Add(-1)
			return
		}

		// grpc.Server only closes listeners passed to Serve, so a never-started
		// server must release its bound listener explicitly.
		cerr := s.listener.Close()
		if cerr != nil {
			s.cfg.logger.With(slog.Any("error", cerr)).Debug("failed closing the grpc server listener")
		}
	})

	if err != nil {
		s.cfg.logger.With(slog.Any("error", err)).Warn("grpc server shutdown completed with error")

		return err
	}

	s.cfg.logger.Debug("grpc server shutdown complete")

	return nil
}

// stop runs GracefulStop and falls back to Stop when ctx expires first. A
// server that was never started has no connections to drain and is stopped
// directly.
func (s *GRPCServer) stop(ctx context.Context) error {
	s.startedMutex.Lock()
	started := s.started
	s.startedMutex.Unlock()

	if !started {
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

// serve starts serving gRPC requests. On any failure other than a normal
// shutdown it releases the wait group and unblocks the monitor goroutine by
// triggering Shutdown, and surfaces the error on the ServeError channel so the
// application is not left waiting on a server that has already died.
func (s *GRPCServer) serve() {
	err := s.grpcServer.Serve(s.listener)
	if err == nil || errors.Is(err, grpc.ErrServerStopped) {
		s.cfg.logger.Debug("closed grpc server")
		return
	}

	s.cfg.logger.With(slog.Any("error", err)).Error("unexpected grpc server failure")

	// Non-blocking publish of the failure (buffer size 1, never overwritten).
	select {
	case s.serveErr <- err:
	default:
	}

	// Ensure the wait group is released and the monitor goroutine unblocked.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.shutdownTimeout)
	defer cancel()

	_ = s.Shutdown(shutdownCtx)
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
