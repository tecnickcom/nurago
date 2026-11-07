package grpcclient

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"
)

var _ grpcutil.ClientInstrumenter = (*testInstrumenter)(nil)

// instrumentedRPC is what a testInstrumenter interceptor observed.
type instrumentedRPC struct {
	method      string
	traceID     string
	code        codes.Code
	hasDeadline bool
}

// testInstrumenter records the RPCs seen by its interceptors.
type testInstrumenter struct {
	mu   sync.Mutex
	rpcs []instrumentedRPC
}

func (ti *testInstrumenter) DialOptions() []grpc.DialOption {
	unary := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		ti.record(ctx, method, err)

		return err
	}

	stream := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		cs, err := streamer(ctx, desc, cc, method, opts...)
		ti.record(ctx, method, err)

		return cs, err
	}

	return []grpc.DialOption{grpc.WithChainUnaryInterceptor(unary), grpc.WithChainStreamInterceptor(stream)}
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

func TestWithInstrumenter_interceptorPosition(t *testing.T) {
	t.Parallel()

	ti := &testInstrumenter{}

	client := newTestClient(t, &testService{}, WithInstrumenter(ti))

	// The context carries no deadline, so only the default one could apply.
	ctx := t.Context()

	_, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{
		ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.NotFound)},
	})
	require.Equal(t, codes.NotFound, status.Code(err))

	stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.Error(t, err)

	rpcs := ti.observed()
	require.Len(t, rpcs, 2)

	for _, r := range rpcs {
		require.True(t, traceid.Valid(r.traceID), "the instrumenter runs after the trace propagation")
		require.False(t, r.hasDeadline, "the instrumenter runs before the default deadline")
	}

	require.Equal(t, methodUnaryCall, rpcs[0].method)
	require.Equal(t, codes.NotFound, rpcs[0].code)
	require.Equal(t, "/grpc.testing.TestService/StreamingOutputCall", rpcs[1].method)
}
