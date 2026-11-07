package grpcserver

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"
)

var (
	_ grpcutil.ServerInstrumenter = (*testInstrumenter)(nil)
	_ grpcutil.ServerInitializer  = (*testInstrumenter)(nil)
)

// instrumentedRPC is what a testInstrumenter interceptor observed.
type instrumentedRPC struct {
	method      string
	traceID     string
	code        codes.Code
	hasDeadline bool
}

// testInstrumenter records the RPCs seen by its interceptors and the services
// passed to InitializeServer.
type testInstrumenter struct {
	mu       sync.Mutex
	rpcs     []instrumentedRPC
	services []string
}

func (ti *testInstrumenter) ServerOptions() []grpc.ServerOption {
	unary := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		ti.record(ctx, info.FullMethod, err)

		return resp, err
	}

	stream := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)
		ti.record(ss.Context(), info.FullMethod, err)

		return err
	}

	return []grpc.ServerOption{grpc.ChainUnaryInterceptor(unary), grpc.ChainStreamInterceptor(stream)}
}

func (ti *testInstrumenter) InitializeServer(s *grpc.Server) {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	for name := range s.GetServiceInfo() {
		ti.services = append(ti.services, name)
	}
}

func (ti *testInstrumenter) record(ctx context.Context, method string, err error) {
	_, hasDeadline := ctx.Deadline()

	ti.mu.Lock()
	defer ti.mu.Unlock()

	ti.rpcs = append(ti.rpcs, instrumentedRPC{
		method:      method,
		traceID:     traceid.FromContext(ctx, ""),
		code:        status.Code(err),
		hasDeadline: hasDeadline,
	})
}

func (ti *testInstrumenter) observed() []instrumentedRPC {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	return append([]instrumentedRPC(nil), ti.rpcs...)
}

// statsOnlyInstrumenter supplies no interceptors and does not implement
// grpcutil.ServerInitializer.
type statsOnlyInstrumenter struct{}

func (statsOnlyInstrumenter) ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{grpc.MaxConcurrentStreams(100)}
}

func TestWithInstrumenter_interceptorPosition(t *testing.T) {
	t.Parallel()

	ti := &testInstrumenter{}

	_, client := newTestServer(t, &testService{},
		WithInstrumenter(ti),
		WithInstrumenter(statsOnlyInstrumenter{}),
		WithRequestTimeout(time.Minute),
	)

	// The RPCs carry no client deadline, so only the default one could apply.
	ctx := t.Context()

	_, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	_, err = client.UnaryCall(ctx, &testgrpc.SimpleRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: panicCode},
	})
	require.Equal(t, codes.Internal, status.Code(err))

	stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Error(t, err)

	rpcs := ti.observed()
	require.Len(t, rpcs, 3)

	for _, r := range rpcs {
		require.True(t, traceid.Valid(r.traceID), "the instrumenter runs after the request inject")
		require.False(t, r.hasDeadline, "the instrumenter runs before the default deadline")
	}

	require.Equal(t, codes.OK, rpcs[0].code)
	require.Equal(t, codes.Internal, rpcs[1].code, "the instrumenter runs outside the panic recovery")
	require.Equal(t, methodStreamingOutputCall, rpcs[2].method)
}

func TestWithInstrumenter_initializeServer(t *testing.T) {
	t.Parallel()

	ti := &testInstrumenter{}

	srv, err := New(t.Context(), &serviceBinder{svc: &testService{}},
		WithLogger(discardLogger()),
		WithServerAddr("127.0.0.1:0"),
		WithReflection(),
		WithInstrumenter(ti),
	)
	require.NoError(t, err)

	t.Cleanup(func() { _ = srv.Shutdown(context.WithoutCancel(t.Context())) })

	ti.mu.Lock()
	defer ti.mu.Unlock()

	require.ElementsMatch(t, []string{
		"grpc.testing.TestService",
		"grpc.reflection.v1.ServerReflection",
		"grpc.reflection.v1alpha.ServerReflection",
	}, ti.services)
}

// panicInstrumenter supplies interceptors that panic.
type panicInstrumenter struct{}

func (panicInstrumenter) ServerOptions() []grpc.ServerOption {
	unary := func(_ context.Context, _ any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
		panic("unary instrumenter panic")
	}

	stream := func(_ any, _ grpc.ServerStream, _ *grpc.StreamServerInfo, _ grpc.StreamHandler) error {
		panic("stream instrumenter panic")
	}

	return []grpc.ServerOption{grpc.ChainUnaryInterceptor(unary), grpc.ChainStreamInterceptor(stream)}
}

func TestWithInstrumenter_panic(t *testing.T) {
	t.Parallel()

	logger, sink := newTestLogger(slog.LevelInfo)

	_, client := newTestServer(t, &testService{}, WithInstrumenter(panicInstrumenter{}), WithLogger(logger))

	ctx := t.Context()

	_, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Equal(t, codes.Internal, status.Code(err))

	// The server is still serving.
	_, err = client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	require.Len(t, sink.entries(t, "panic"), 3)

	requests := sink.entries(t, "request")
	require.Len(t, requests, 3, "the request logger records the recovered RPCs")

	for _, e := range requests {
		require.Equal(t, codes.Internal.String(), e["grpc_code"])
	}
}
