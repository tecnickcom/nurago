package grpcserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
)

func TestNopBinder(t *testing.T) {
	t.Parallel()
	require.NotNil(t, NopBinder())
}

func Test_nopBinder_BindGRPC(t *testing.T) {
	t.Parallel()
	require.NoError(t, NopBinder().BindGRPC(t.Context(), nil))
}

func TestNew_nilBinder(t *testing.T) {
	t.Parallel()

	s, err := New(t.Context(), nil, WithServerAddr("127.0.0.1:0"))
	require.ErrorIs(t, err, ErrNilBinder)
	require.Nil(t, s)
}

func TestNew_nilContext(t *testing.T) {
	t.Parallel()

	s, err := New(nil, NopBinder(), WithServerAddr("127.0.0.1:0"), WithLogger(discardLogger())) //nolint:staticcheck // deliberately nil
	require.NoError(t, err)
	require.NotNil(t, s)
	require.NoError(t, s.Shutdown(t.Context()))
}

func TestNew_optionError(t *testing.T) {
	t.Parallel()

	s, err := New(t.Context(), NopBinder(), WithServerAddr("invalid"))
	require.Error(t, err)
	require.Nil(t, s)
}

func TestNew_nilOption(t *testing.T) {
	t.Parallel()

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), nil)
	require.ErrorContains(t, err, "option is required")
	require.Nil(t, s)
}

func TestNew_invalidTLSConfig(t *testing.T) {
	t.Parallel()

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	require.ErrorIs(t, err, ErrInvalidTLSConfig)
	require.Nil(t, s)
}

func TestNew_tapHandleConflict(t *testing.T) {
	t.Parallel()

	tapOpt := WithServerOption(grpc.InTapHandle(func(ctx context.Context, _ *tap.Info) (context.Context, error) {
		return ctx, nil
	}))

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), tapOpt, WithMethodTimeout(methodUnaryCall, time.Second))
	require.ErrorIs(t, err, ErrInvalidServerOption)
	require.Nil(t, s)

	s, err = New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), tapOpt, WithMethodTimeout(methodUnaryCall, -1))
	require.NoError(t, err)
	require.NoError(t, s.Shutdown(t.Context()))
}

func TestNew_logsBoundAddr(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), WithLogger(logger))
	require.NoError(t, err)

	s.StartServer()
	require.NoError(t, s.Shutdown(t.Context()))

	entries := sink.entries(t, "listening for grpc requests")
	require.Len(t, entries, 1)
	require.Equal(t, s.Addr().String(), entries[0]["addr"])
	require.NotEqual(t, "127.0.0.1:0", entries[0]["addr"])
}

// unexpectedBinder fails the test when BindGRPC is called.
type unexpectedBinder struct {
	t *testing.T
}

func (b unexpectedBinder) BindGRPC(_ context.Context, _ grpc.ServiceRegistrar) error {
	b.t.Error("BindGRPC must not be called")
	return nil
}

func TestNew_lifecycleError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  Option
	}{
		{name: "shutdown timeout", opt: WithShutdownTimeout(0)},
		{name: "shutdown wait group", opt: WithShutdownWaitGroup(nil)},
		{name: "shutdown signal channel", opt: WithShutdownSignalChan(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var lc net.ListenConfig

			// The address stays bound: the options must be rejected before
			// New tries to listen on it.
			busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)

			t.Cleanup(func() { _ = busy.Close() })

			addr := busy.Addr().String()

			s, err := New(t.Context(), unexpectedBinder{t: t}, WithServerAddr(addr), WithLogger(discardLogger()), tt.opt)
			require.ErrorContains(t, err, "failed creating the grpc server lifecycle")
			require.Nil(t, s)
		})
	}
}

// failAfterFirstCall returns a function that calls first on its first
// invocation and then on every later one.
func failAfterFirstCall[T any](first, then func(T) error) func(T) error {
	var called atomic.Bool

	return func(c T) error {
		if called.Swap(true) {
			return then(c)
		}

		return first(c)
	}
}

func TestNew_lifecycleNewError(t *testing.T) {
	t.Parallel()

	// The option passes the validation in newConfig and fails in srvlifecycle.New.
	opt := func(cfg *config) error {
		cfg.lifecycleOpts = append(cfg.lifecycleOpts, failAfterFirstCall(
			srvlifecycle.WithShutdownTimeout(time.Second),
			srvlifecycle.WithShutdownTimeout(0),
		))

		return nil
	}

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	s, err := New(t.Context(), unexpectedBinder{t: t}, WithServerAddr(addr), WithLogger(discardLogger()), opt)
	require.ErrorContains(t, err, "failed creating the grpc server lifecycle")
	require.Nil(t, s)

	l, err = lc.Listen(t.Context(), "tcp", addr)
	require.NoError(t, err, "the listener must be released")
	require.NoError(t, l.Close())
}

func TestNew_listenError(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = busy.Close() })

	s, err := New(t.Context(), NopBinder(), WithServerAddr(busy.Addr().String()), WithLogger(discardLogger()))
	require.ErrorContains(t, err, "failed creating the grpc server address listener")
	require.Nil(t, s)
}

// otherService is a handler type that does not implement any service interface.
type otherService struct{}

func TestNew_registrationErrors(t *testing.T) {
	t.Parallel()

	errBind := errors.New("bind failure")

	descWith := func(handlerType any) *grpc.ServiceDesc {
		d := testgrpc.TestService_ServiceDesc
		d.HandlerType = handlerType

		return &d
	}

	tests := []struct {
		name    string
		bind    funcBinder
		opts    []Option
		wantErr error
		wantMsg string
	}{
		{
			name:    "binder error",
			bind:    func(_ context.Context, _ grpc.ServiceRegistrar) error { return errBind },
			wantErr: errBind,
		},
		{
			name: "duplicate service",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				testgrpc.RegisterTestServiceServer(s, &testService{})
				testgrpc.RegisterTestServiceServer(s, &testService{})

				return nil
			},
			wantMsg: `duplicate service "grpc.testing.TestService"`,
		},
		{
			name: "registrations after a failure are ignored",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				s.RegisterService(nil, nil)
				testgrpc.RegisterTestServiceServer(s, &testService{})

				return nil
			},
			wantMsg: "nil service descriptor",
		},
		{
			name: "handler does not implement the service",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				s.RegisterService(&testgrpc.TestService_ServiceDesc, &otherService{})
				return nil
			},
			wantMsg: "does not implement",
		},
		{
			name: "nil handler type",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				s.RegisterService(descWith(nil), &testService{})
				return nil
			},
			wantMsg: "invalid HandlerType",
		},
		{
			name: "non-pointer handler type",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				s.RegisterService(descWith(1), &testService{})
				return nil
			},
			wantMsg: "invalid HandlerType",
		},
		{
			name: "pointer to non-interface handler type",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				s.RegisterService(descWith((*int)(nil)), &testService{})
				return nil
			},
			wantMsg: "invalid HandlerType",
		},
		{
			name: "reflection registered twice",
			bind: func(_ context.Context, s grpc.ServiceRegistrar) error {
				gs, ok := s.(reflection.GRPCServer)
				require.True(t, ok)
				reflection.Register(gs)

				return nil
			},
			opts:    []Option{WithReflection()},
			wantMsg: "duplicate service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]Option{WithServerAddr("127.0.0.1:0"), WithLogger(discardLogger())}, tt.opts...)

			s, err := New(t.Context(), tt.bind, opts...)
			require.ErrorIs(t, err, ErrServiceRegistration)
			require.Nil(t, s)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}

			if tt.wantMsg != "" {
				require.ErrorContains(t, err, tt.wantMsg)
			}
		})
	}
}

func TestNew_registrationErrorReleasesListener(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	bind := funcBinder(func(context.Context, grpc.ServiceRegistrar) error {
		return errors.New("bind failed")
	})

	s, err := New(t.Context(), bind, WithServerAddr(addr), WithLogger(discardLogger()), WithHealthService(nil))
	require.ErrorIs(t, err, ErrServiceRegistration)
	require.Nil(t, s)

	l, err = lc.Listen(t.Context(), "tcp", addr)
	require.NoError(t, err, "the listener must be released")
	require.NoError(t, l.Close())
}

func TestNew_nilImplementationAllowed(t *testing.T) {
	t.Parallel()

	bind := funcBinder(func(_ context.Context, s grpc.ServiceRegistrar) error {
		s.RegisterService(&testgrpc.TestService_ServiceDesc, nil)
		return nil
	})

	s, err := New(t.Context(), bind, WithServerAddr("127.0.0.1:0"), WithLogger(discardLogger()))
	require.NoError(t, err)
	require.Contains(t, s.GetServiceInfo(), "grpc.testing.TestService")
	require.NoError(t, s.Shutdown(t.Context()))
}

func TestNew_lateRegistrationIgnored(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	var reg grpc.ServiceRegistrar

	bind := funcBinder(func(_ context.Context, s grpc.ServiceRegistrar) error {
		reg = s

		testgrpc.RegisterTestServiceServer(s, &testService{})

		return nil
	})

	s, err := New(t.Context(), bind, WithServerAddr("127.0.0.1:0"), WithLogger(logger))
	require.NoError(t, err)

	s.StartServer()

	t.Cleanup(func() { _ = s.Shutdown(context.WithoutCancel(t.Context())) })

	desc := testgrpc.UnimplementedService_ServiceDesc

	require.NotPanics(t, func() {
		reg.RegisterService(&desc, &testgrpc.UnimplementedUnimplementedServiceServer{})
		reg.RegisterService(nil, nil)
	})
	require.NotContains(t, s.GetServiceInfo(), desc.ServiceName)

	ignored := sink.entries(t, "service registration ignored: registrar closed")
	require.Len(t, ignored, 2)
	require.Equal(t, "grpcserver", ignored[0]["component"])
	require.Equal(t, s.Addr().String(), ignored[0]["addr"])
	require.Equal(t, desc.ServiceName, ignored[0]["grpc_service"])
	require.Empty(t, ignored[1]["grpc_service"])
}

func TestGRPCServer_serves(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	s, client := newTestServer(t, &testService{}, WithShutdownWaitGroup(wg))

	require.NotNil(t, s.Addr())
	require.Contains(t, s.GetServiceInfo(), "grpc.testing.TestService")

	resp, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetUsername())

	require.NoError(t, s.Shutdown(t.Context()))
	waitTimeout(t, wg)
}

func TestStartServer_twiceIsNoop(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelDebug)
	wg := &sync.WaitGroup{}

	s, client := newTestServer(t, &testService{}, WithLogger(logger), WithShutdownWaitGroup(wg))

	s.StartServer()

	require.Len(t, sink.entries(t, "start ignored: server already started or shut down"), 1)

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	require.NoError(t, s.Shutdown(t.Context()))
	waitTimeout(t, wg) // a second Add(1) would block here forever
}

func TestStartServer_afterShutdownIsNoop(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelDebug)
	wg := &sync.WaitGroup{}

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), WithLogger(logger), WithShutdownWaitGroup(wg))
	require.NoError(t, err)

	require.NoError(t, s.Shutdown(t.Context()))

	s.StartServer()

	require.Len(t, sink.entries(t, "start ignored: server already started or shut down"), 1)
	waitTimeout(t, wg)
}

func TestShutdown_beforeStartReleasesListener(t *testing.T) {
	t.Parallel()

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), WithLogger(discardLogger()))
	require.NoError(t, err)

	addr := s.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, s.Shutdown(ctx), "a never-started server has nothing to drain")

	var lc net.ListenConfig

	ls, err := lc.Listen(t.Context(), "tcp", addr)
	require.NoError(t, err, "the listener must be released")
	require.NoError(t, ls.Close())
}

func TestShutdown_idempotent(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	s, _ := newTestServer(t, &testService{}, WithShutdownWaitGroup(wg))

	require.NoError(t, s.Shutdown(t.Context()))
	require.NoError(t, s.Shutdown(t.Context()))
	waitTimeout(t, wg) // a second decrement would panic with a negative counter

	select {
	case <-s.lc.Done():
	default:
		require.Fail(t, "the done channel is not closed after Shutdown")
	}
}

func TestShutdown_signalChan(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}
	sc := make(chan struct{})

	_, _ = newTestServer(t, &testService{}, WithShutdownWaitGroup(wg), WithShutdownSignalChan(sc))

	close(sc)
	waitTimeout(t, wg)
}

func TestShutdown_contextCanceled(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	ctx, cancel := context.WithCancel(t.Context())

	_, _ = newTestServerCtx(t, ctx, &testService{}, WithShutdownWaitGroup(wg))

	cancel()
	waitTimeout(t, wg)
}

func TestStartServerCtx_canceled(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), WithLogger(discardLogger()), WithShutdownWaitGroup(wg))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	s.StartServerCtx(ctx)
	cancel()
	waitTimeout(t, wg)
}

func TestStartServerCtx_nilContext(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	s, err := New(t.Context(), NopBinder(), WithServerAddr("127.0.0.1:0"), WithLogger(discardLogger()), WithShutdownWaitGroup(wg))
	require.NoError(t, err)

	//nolint:staticcheck // a nil context is the case under test
	s.StartServerCtx(nil)
	require.NoError(t, s.Shutdown(t.Context()))
	waitTimeout(t, wg)
}

func TestShutdown_timeoutStopsServer(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}
	svc := &testService{block: make(chan struct{}), started: make(chan struct{}, 1)}

	s, client := newTestServer(t, svc, WithShutdownWaitGroup(wg))

	t.Cleanup(func() { close(svc.block) })

	stream, err := client.FullDuplexCall(rpcContext(t))
	require.NoError(t, err)

	<-svc.started

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err = s.Shutdown(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "graceful shutdown interrupted")

	_, err = stream.Recv()
	require.Equal(t, codes.Unavailable, status.Code(err), "the connection must be closed by Stop")

	waitTimeout(t, wg)
}

func TestServeError(t *testing.T) {
	t.Parallel()

	wg := &sync.WaitGroup{}

	var lc net.ListenConfig

	// A closed listener makes Serve fail immediately.
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, l.Close())

	cfg := defaultConfig()
	cfg.logger = discardLogger()
	require.NoError(t, WithShutdownWaitGroup(wg)(cfg))

	s := &GRPCServer{cfg: cfg, ctx: t.Context(), grpcServer: grpc.NewServer()}

	s.lc, err = srvlifecycle.New(lifecycleServer{s: s}, l, cfg.lifecycleOptions()...)
	require.NoError(t, err)

	s.StartServer()

	select {
	case err := <-s.ServeError():
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(10 * time.Second):
		require.Fail(t, "timed out waiting for the serve error")
	}

	waitTimeout(t, wg)

	require.Empty(t, s.ServeError(), "the error is published once")
}

func TestTLS(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := makeTestCert(t)

	s, _ := newTestServer(t, &testService{}, WithTLSCertData(certPEM, keyPEM))

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certPEM))

	creds := credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
	client := newTestClient(t, s.Addr().String(), grpc.WithTransportCredentials(creds))

	_, err := client.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	plain := newTestClient(t, s.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))

	_, err = plain.UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.Equal(t, codes.Unavailable, status.Code(err), "a plaintext client must not reach a TLS server")
}

func TestReflection(t *testing.T) {
	t.Parallel()

	s, _ := newTestServer(t, &testService{}, WithReflection())

	info := s.GetServiceInfo()
	require.Contains(t, info, "grpc.reflection.v1.ServerReflection")
	require.Contains(t, info, "grpc.reflection.v1alpha.ServerReflection")

	conn, err := grpc.NewClient(s.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(rpcContext(t))
	require.NoError(t, err)

	err = stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{},
	})
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)

	services := resp.GetListServicesResponse().GetService()
	names := make([]string, 0, len(services))

	for _, svc := range services {
		names = append(names, svc.GetName())
	}

	require.True(t, slices.Contains(names, "grpc.testing.TestService"), "listed services: %v", names)

	require.NoError(t, stream.CloseSend())

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestReflection_disabledByDefault(t *testing.T) {
	t.Parallel()

	s, _ := newTestServer(t, &testService{})

	require.NotContains(t, s.GetServiceInfo(), "grpc.reflection.v1.ServerReflection")
}

func TestWaitStop(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	closed := make(chan struct{})
	close(closed)

	forced := 0
	forceStop := func() { forced++ }

	for range 100 {
		require.NoError(t, waitStop(canceled, closed, forceStop), "a completed graceful stop is not an error")
	}

	require.Zero(t, forced)

	require.NoError(t, waitStop(t.Context(), closed, forceStop))
	require.Zero(t, forced)

	err := waitStop(canceled, make(chan struct{}), forceStop)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, forced)
}
