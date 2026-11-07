package grpcserver

import (
	"context"
	"crypto/tls"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func TestWithServerAddr(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithServerAddr("::")(cfg))

	v := ":1234"
	require.NoError(t, WithServerAddr(v)(cfg))
	require.Equal(t, v, cfg.serverAddr)
}

func TestWithRequestTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithRequestTimeout(0)(cfg))
	require.Error(t, WithRequestTimeout(-1)(cfg))

	v := 3 * time.Minute
	require.NoError(t, WithRequestTimeout(v)(cfg))
	require.Equal(t, v, cfg.requestTimeout)
}

func TestWithMethodTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.ErrorIs(t, WithMethodTimeout("bad", time.Second)(cfg), ErrInvalidFullMethod)
	require.Error(t, WithMethodTimeout(methodUnaryCall, 0)(cfg))

	require.NoError(t, WithMethodTimeout(methodUnaryCall, time.Second)(cfg))
	require.NoError(t, WithMethodTimeout(methodFullDuplexCall, -1)(cfg))
	require.Equal(t, map[string]time.Duration{methodUnaryCall: time.Second, methodFullDuplexCall: -1}, cfg.methodTimeouts)
}

func TestWithContextValues(t *testing.T) {
	t.Parallel()

	type key struct{}

	cfg := defaultConfig()

	require.Error(t, WithContextValues(nil)(cfg))
	require.Error(t, WithContextValues(key{}, []int{1})(cfg))
	require.Empty(t, cfg.contextKeys)

	require.NoError(t, WithContextValues(key{})(cfg))
	require.NoError(t, WithContextValues("k")(cfg))
	require.Equal(t, []any{key{}, "k"}, cfg.contextKeys)
}

func TestWithMaxRecvMsgSize(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithMaxRecvMsgSize(0)(cfg))
	require.NoError(t, WithMaxRecvMsgSize(1024)(cfg))
	require.Equal(t, 1024, cfg.maxRecvMsgSize)
}

func TestWithMaxSendMsgSize(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithMaxSendMsgSize(-1)(cfg))
	require.NoError(t, WithMaxSendMsgSize(2048)(cfg))
	require.Equal(t, 2048, cfg.maxSendMsgSize)
}

func TestWithMaxHeaderListSize(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithMaxHeaderListSize(0)(cfg))
	require.NoError(t, WithMaxHeaderListSize(8192)(cfg))
	require.Equal(t, uint32(8192), cfg.maxHeaderListSize)
}

func TestWithKeepaliveParams(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	v := keepalive.ServerParameters{MaxConnectionIdle: time.Minute, Time: time.Hour, Timeout: 10 * time.Second}
	require.NoError(t, WithKeepaliveParams(v)(cfg))
	require.Equal(t, &v, cfg.keepaliveParams)
}

func TestWithKeepaliveEnforcementPolicy(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	v := keepalive.EnforcementPolicy{MinTime: time.Minute, PermitWithoutStream: true}
	require.NoError(t, WithKeepaliveEnforcementPolicy(v)(cfg))
	require.Equal(t, &v, cfg.keepalivePolicy)
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithShutdownTimeout(0)(cfg))
	require.NoError(t, WithShutdownTimeout(5*time.Second)(cfg))
	require.Equal(t, 5*time.Second, cfg.shutdownTimeout)
}

func TestWithShutdownWaitGroup(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithShutdownWaitGroup(nil)(cfg))

	wg := &sync.WaitGroup{}
	require.NoError(t, WithShutdownWaitGroup(wg)(cfg))
	require.Same(t, wg, cfg.shutdownWaitGroup)
}

func TestWithShutdownSignalChan(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithShutdownSignalChan(nil)(cfg))

	ch := make(chan struct{})
	require.NoError(t, WithShutdownSignalChan(ch)(cfg))
	require.Equal(t, ch, cfg.shutdownSignalChan)
}

func TestWithTLSCertData(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.ErrorContains(t, WithTLSCertData([]byte("bad"), []byte("bad"))(cfg), "failed configuring TLS")
	require.Nil(t, cfg.tlsConfig)

	certPEM, keyPEM := makeTestCert(t)
	require.NoError(t, WithTLSCertData(certPEM, keyPEM)(cfg))
	require.NotNil(t, cfg.tlsConfig)
	require.Len(t, cfg.tlsConfig.Certificates, 1)
	require.Equal(t, uint16(tls.VersionTLS12), cfg.tlsConfig.MinVersion)
}

func TestWithTLSConfig(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithTLSConfig(nil)(cfg))

	v := &tls.Config{MinVersion: tls.VersionTLS13}
	require.NoError(t, WithTLSConfig(v)(cfg))
	require.Same(t, v, cfg.tlsConfig)
}

func TestWithReflection(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.False(t, cfg.reflection)
	require.NoError(t, WithReflection()(cfg))
	require.True(t, cfg.reflection)
}

func TestWithTraceIDMetadataKey(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.ErrorIs(t, WithTraceIDMetadataKey("")(cfg), ErrInvalidMetadataKey)
	require.NoError(t, WithTraceIDMetadataKey("X-Correlation-ID")(cfg))
	require.Equal(t, "x-correlation-id", cfg.traceIDMetadataKey)
}

func TestWithRedactFn(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithRedactFn(nil)(cfg))
	require.NoError(t, WithRedactFn(func(_ []byte) string { return "x" })(cfg))
	require.Equal(t, "x", cfg.redactFn(nil))
}

func TestWithMaxDumpSize(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Equal(t, defaultMaxDumpSize, cfg.maxDumpSize)
	require.Error(t, WithMaxDumpSize(0)(cfg))
	require.NoError(t, WithMaxDumpSize(512)(cfg))
	require.Equal(t, 512, cfg.maxDumpSize)
}

func TestWithLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithLogger(nil)(cfg))

	l := slog.New(slog.DiscardHandler)
	require.NoError(t, WithLogger(l)(cfg))
	require.Same(t, l, cfg.logger)
}

func TestWithoutRequestLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.True(t, cfg.isLoggerEnabled(methodUnaryCall))
	require.NoError(t, WithoutRequestLogger()(cfg))
	require.False(t, cfg.isLoggerEnabled(methodUnaryCall))
}

func TestWithoutMethodLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.ErrorIs(t, WithoutMethodLogger(methodUnaryCall, "bad")(cfg), ErrInvalidFullMethod)
	require.Empty(t, cfg.disabledMethodLogger, "a failed option must not partially apply")

	require.NoError(t, WithoutMethodLogger(methodUnaryCall, methodFullDuplexCall)(cfg))
	require.False(t, cfg.isLoggerEnabled(methodUnaryCall))
	require.False(t, cfg.isLoggerEnabled(methodFullDuplexCall))
	require.True(t, cfg.isLoggerEnabled(methodStreamingInputCall))
}

func TestWithPanicHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithPanicHandlerFunc(nil)(cfg))

	called := false
	fn := func(_ context.Context, _ any) error {
		called = true
		return nil
	}

	require.NoError(t, WithPanicHandlerFunc(fn)(cfg))
	require.NoError(t, cfg.panicHandlerFunc(t.Context(), nil))
	require.True(t, called)
}

func TestWithUnaryInterceptor(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	i := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(ctx, req)
	}

	require.Error(t, WithUnaryInterceptor(i, nil)(cfg))
	require.Empty(t, cfg.unaryInterceptors)

	require.NoError(t, WithUnaryInterceptor(i, i)(cfg))
	require.NoError(t, WithUnaryInterceptor(i)(cfg))
	require.Len(t, cfg.unaryInterceptors, 3)
}

func TestWithStreamInterceptor(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	i := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, ss)
	}

	require.Error(t, WithStreamInterceptor(nil)(cfg))
	require.Empty(t, cfg.streamInterceptors)

	require.NoError(t, WithStreamInterceptor(i)(cfg))
	require.Len(t, cfg.streamInterceptors, 1)
}

func TestWithServerOption(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithServerOption(grpc.MaxRecvMsgSize(1), nil)(cfg))
	require.Empty(t, cfg.extraServerOptions)

	require.NoError(t, WithServerOption(grpc.MaxRecvMsgSize(1), grpc.MaxSendMsgSize(1))(cfg))
	require.Len(t, cfg.extraServerOptions, 2)
}
