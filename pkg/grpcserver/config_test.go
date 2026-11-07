package grpcserver

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Equal(t, ":8019", cfg.serverAddr)
	require.Equal(t, "x-request-id", cfg.traceIDMetadataKey)
	require.Equal(t, 30*time.Second, cfg.shutdownTimeout)
	require.Zero(t, cfg.requestTimeout)
	require.False(t, cfg.reflection)
	require.NotNil(t, cfg.redactFn)
	require.NotNil(t, cfg.logger)
	require.NotNil(t, cfg.shutdownWaitGroup)
	require.NotNil(t, cfg.shutdownSignalChan)
	require.NotNil(t, cfg.rnd)
	require.Empty(t, cfg.contextKeys)
	require.Empty(t, cfg.baseValues)
}

func TestDefaultPanicHandler(t *testing.T) {
	t.Parallel()

	err := defaultPanicHandler(t.Context(), "boom")
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, codes.Internal.String(), status.Convert(err).Message())
}

func TestServerOptions(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	require.Len(t, cfg.serverOptions(), 2, "interceptor chains only")

	certPEM, keyPEM := makeTestCert(t)

	opts := []Option{
		WithTLSCertData(certPEM, keyPEM),
		WithMaxRecvMsgSize(1),
		WithMaxSendMsgSize(1),
		WithMaxHeaderListSize(1),
		WithKeepaliveParams(keepalive.ServerParameters{}),
		WithKeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{}),
		WithServerOption(grpc.ConnectionTimeout(time.Second)),
	}

	for _, o := range opts {
		require.NoError(t, o(cfg))
	}

	require.Len(t, cfg.serverOptions(), 9)

	require.NoError(t, WithMethodTimeout(methodUnaryCall, -1)(cfg))
	require.Len(t, cfg.serverOptions(), 9, "a disabled method timeout installs no tap handle")

	require.NoError(t, WithMethodTimeout(methodFullDuplexCall, time.Second)(cfg))
	require.Len(t, cfg.serverOptions(), 10, "a method timeout installs the tap handle")
}

func TestValidateTLSConfig(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateTLSConfig(nil))
	require.ErrorIs(t, validateTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}), ErrInvalidTLSConfig)

	getCert := func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil }
	require.NoError(t, validateTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: getCert}))

	getCfg := func(*tls.ClientHelloInfo) (*tls.Config, error) { return nil, nil } //nolint:nilnil
	require.NoError(t, validateTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: getCfg}))

	require.NoError(t, validateTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{}}}))
}

func TestMethodTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.methodTimeouts[methodUnaryCall] = time.Second

	require.Equal(t, time.Second, cfg.methodTimeout(methodUnaryCall, time.Hour))
	require.Equal(t, time.Hour, cfg.methodTimeout(methodFullDuplexCall, time.Hour))
}

func TestValidateAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr    string
		wantErr bool
	}{
		{addr: ":8019"},
		{addr: ":0"},
		{addr: "localhost:8019"},
		{addr: "0.0.0.0:8019"},
		{addr: "[::1]:8019"},
		{addr: ":65535"},
		{addr: "", wantErr: true},
		{addr: "::", wantErr: true},
		{addr: "localhost", wantErr: true},
		{addr: ":port", wantErr: true},
		{addr: ":-1", wantErr: true},
		{addr: ":65536", wantErr: true},
	}

	for _, tt := range tests {
		err := validateAddr(tt.addr)
		if tt.wantErr {
			require.Error(t, err, tt.addr)
			continue
		}

		require.NoError(t, err, tt.addr)
	}
}

func TestValidateFullMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "/pkg.Service/Method"},
		{name: "/Service/Method"},
		{name: "", wantErr: true},
		{name: "/", wantErr: true},
		{name: "pkg.Service/Method", wantErr: true},
		{name: "/pkg.Service", wantErr: true},
		{name: "/pkg.Service/", wantErr: true},
		{name: "//Method", wantErr: true},
		{name: "/pkg.Service/Method/Extra", wantErr: true},
	}

	for _, tt := range tests {
		err := validateFullMethod(tt.name)
		if tt.wantErr {
			require.ErrorIs(t, err, ErrInvalidFullMethod, tt.name)
			continue
		}

		require.NoError(t, err, tt.name)
	}
}

func TestNormalizeMetadataKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key     string
		want    string
		wantErr bool
	}{
		{key: "x-request-id", want: "x-request-id"},
		{key: "X-Request-ID", want: "x-request-id"},
		{key: "trace_id.v2", want: "trace_id.v2"},
		{key: "", wantErr: true},
		{key: "grpc-trace", wantErr: true},
		{key: "GRPC-Trace", wantErr: true},
		{key: "trace-bin", wantErr: true},
		{key: "user-agent", wantErr: true},
		{key: "Content-Type", wantErr: true},
		{key: "te", wantErr: true},
		{key: "trace id", wantErr: true},
		{key: "trace:id", wantErr: true},
		{key: "tràce", wantErr: true},
	}

	for _, tt := range tests {
		got, err := normalizeMetadataKey(tt.key)
		if tt.wantErr {
			require.ErrorIs(t, err, ErrInvalidMetadataKey, tt.key)
			require.Empty(t, got, tt.key)

			continue
		}

		require.NoError(t, err, tt.key)
		require.Equal(t, tt.want, got, tt.key)
	}
}
