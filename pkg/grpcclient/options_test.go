package grpcclient

import (
	"context"
	"crypto/tls"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/stats"
)

// testCreds is a per-RPC credentials implementation for tests.
type testCreds struct {
	secure bool
}

func (c testCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer test"}, nil
}

func (c testCreds) RequireTransportSecurity() bool { return c.secure }

// testStatsHandler is a no-op stats handler.
type testStatsHandler struct{}

func (testStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (testStatsHandler) HandleRPC(_ context.Context, _ stats.RPCStats)                   {}
func (testStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (testStatsHandler) HandleConn(_ context.Context, _ stats.ConnStats) {}

func TestWithTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.NoError(t, WithTimeout(3*time.Second)(cfg))
	require.Equal(t, 3*time.Second, cfg.timeout)

	require.NoError(t, WithTimeout(-1)(cfg))
	require.Equal(t, time.Duration(-1), cfg.timeout)
}

func TestWithLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithLogger(nil)(cfg))

	l := slog.New(slog.DiscardHandler)
	require.NoError(t, WithLogger(l)(cfg))
	require.Same(t, l, cfg.logger)
}

func TestWithComponent(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithComponent("")(cfg))
	require.NoError(t, WithComponent("svc")(cfg))
	require.Equal(t, "svc", cfg.component)
}

func TestWithLogPrefix(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.NoError(t, WithLogPrefix("p_")(cfg))
	require.Equal(t, "p_", cfg.logPrefix)
}

func TestWithTraceIDMetadataKey(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.ErrorIs(t, WithTraceIDMetadataKey("")(cfg), ErrInvalidMetadataKey)
	require.ErrorIs(t, WithTraceIDMetadataKey("grpc-x")(cfg), ErrInvalidMetadataKey)
	require.ErrorIs(t, WithTraceIDMetadataKey("x-bin")(cfg), ErrInvalidMetadataKey)

	require.NoError(t, WithTraceIDMetadataKey("X-Trace-ID")(cfg))
	require.Equal(t, "x-trace-id", cfg.traceIDMetadataKey)
}

func TestWithRedactFn(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithRedactFn(nil)(cfg))
	require.NoError(t, WithRedactFn(func(_ []byte) string { return "r" })(cfg))
	require.Equal(t, "r", cfg.redactFn(nil))
}

func TestWithMaxDumpSize(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithMaxDumpSize(0)(cfg))
	require.NoError(t, WithMaxDumpSize(10)(cfg))
	require.Equal(t, 10, cfg.maxDumpSize)
}

func TestWithTLSConfig(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithTLSConfig(nil)(cfg))

	tc := &tls.Config{MinVersion: tls.VersionTLS13}
	require.NoError(t, WithTLSConfig(tc)(cfg))
	require.Same(t, tc, cfg.tlsConfig)
}

func TestWithInsecure(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.NoError(t, WithInsecure()(cfg))
	require.True(t, cfg.insecure)
}

func TestWithCredentialsBundle(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithCredentialsBundle(nil)(cfg))
	require.NoError(t, WithCredentialsBundle(testBundle{})(cfg))
	require.Equal(t, testBundle{}, cfg.credsBundle)
}

func TestWithMaxCallAttempts(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithMaxCallAttempts(1)(cfg))
	require.NoError(t, WithMaxCallAttempts(8)(cfg))
	require.Equal(t, 8, cfg.maxCallAttempts)
}

func TestWithRetryPolicy(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.ErrorIs(t, WithRetryPolicy(RetryPolicy{})(cfg), ErrInvalidRetryPolicy)
	require.NoError(t, WithRetryPolicy(validRetryPolicy())(cfg))
	require.Len(t, cfg.retryPolicies, 1)
}

func TestWithKeepaliveParams(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	kp := keepalive.ClientParameters{Time: time.Minute}
	require.NoError(t, WithKeepaliveParams(kp)(cfg))
	require.Equal(t, &kp, cfg.keepaliveParams)
}

func TestWithMaxMsgSize(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithMaxRecvMsgSize(0)(cfg))
	require.Error(t, WithMaxSendMsgSize(-1)(cfg))

	require.NoError(t, WithMaxRecvMsgSize(10)(cfg))
	require.NoError(t, WithMaxSendMsgSize(20)(cfg))
	require.Equal(t, 10, cfg.maxRecvMsgSize)
	require.Equal(t, 20, cfg.maxSendMsgSize)
}

func TestWithPerRPCCredentials(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithPerRPCCredentials(nil)(cfg))
	require.NoError(t, WithPerRPCCredentials(testCreds{})(cfg))
	require.Len(t, cfg.perRPCCredentials, 1)
}

func TestWithStatsHandler(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithStatsHandler(nil)(cfg))
	require.NoError(t, WithStatsHandler(testStatsHandler{})(cfg))
	require.Len(t, cfg.statsHandlers, 1)
}

func TestWithInterceptors(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	unary := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	stream := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(ctx, desc, cc, method, opts...)
	}

	require.Error(t, WithUnaryInterceptor(unary, nil)(cfg))
	require.Error(t, WithStreamInterceptor(nil)(cfg))
	require.Empty(t, cfg.unaryInterceptors)

	require.NoError(t, WithUnaryInterceptor(unary, unary)(cfg))
	require.NoError(t, WithStreamInterceptor(stream)(cfg))
	require.Len(t, cfg.unaryInterceptors, 2)
	require.Len(t, cfg.streamInterceptors, 1)
}

func TestWithDialOption(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithDialOption(grpc.WithUserAgent("x"), nil)(cfg))
	require.Empty(t, cfg.extraDialOptions)

	require.NoError(t, WithDialOption(grpc.WithUserAgent("x"))(cfg))
	require.Len(t, cfg.extraDialOptions, 1)
}
