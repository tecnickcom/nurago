package grpcclient

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Equal(t, "-", cfg.component)
	require.Empty(t, cfg.logPrefix)
	require.Equal(t, grpcutil.DefaultTraceIDMetadataKey, cfg.traceIDMetadataKey)
	require.Equal(t, time.Minute, cfg.timeout)
	require.Equal(t, defaultMaxDumpSize, cfg.maxDumpSize)
	require.NotNil(t, cfg.redactFn)
	require.NotNil(t, cfg.logger)
	require.NotNil(t, cfg.rnd)
	require.False(t, cfg.insecure)
	require.Nil(t, cfg.tlsConfig)
	require.Empty(t, cfg.retryPolicies)
}

func TestDialOptions(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	opts, err := cfg.dialOptions()
	require.NoError(t, err)
	require.Len(t, opts, 3, "credentials and interceptor chains only")

	p := validRetryPolicy()
	p.MaxAttempts = 9

	all := []Option{
		WithRetryPolicy(p),
		WithMaxCallAttempts(9),
		WithMaxRecvMsgSize(1),
		WithMaxSendMsgSize(1),
		WithKeepaliveParams(keepalive.ClientParameters{}),
		WithStatsHandler(testStatsHandler{}),
		WithPerRPCCredentials(testCreds{}),
		WithDialOption(grpc.WithUserAgent("x")),
	}

	for _, o := range all {
		require.NoError(t, o(cfg))
	}

	opts, err = cfg.dialOptions()
	require.NoError(t, err)
	require.Len(t, opts, 3+2+4+1)
}

func TestDialOptions_maxAttemptsAboveCap(t *testing.T) {
	t.Parallel()

	p := validRetryPolicy()
	p.MaxAttempts = 6

	cfg := defaultConfig()
	require.NoError(t, WithRetryPolicy(p)(cfg))

	_, err := cfg.dialOptions()
	require.ErrorIs(t, err, ErrInvalidRetryPolicy, "above the default cap")

	require.NoError(t, WithMaxCallAttempts(5)(cfg))

	_, err = cfg.dialOptions()
	require.ErrorIs(t, err, ErrInvalidRetryPolicy, "above an explicit cap")

	require.NoError(t, WithMaxCallAttempts(6)(cfg))

	_, err = cfg.dialOptions()
	require.NoError(t, err)
}

// testBundle is a credentials.Bundle with insecure transport credentials.
type testBundle struct{}

func (testBundle) TransportCredentials() credentials.TransportCredentials {
	return insecure.NewCredentials()
}

func (testBundle) PerRPCCredentials() credentials.PerRPCCredentials { return nil }

func (b testBundle) NewWithMode(string) (credentials.Bundle, error) { return b, nil }

func TestNew_credentialsBundle(t *testing.T) {
	t.Parallel()

	conn, err := New(bufTarget,
		WithCredentialsBundle(testBundle{}),
		WithLogger(discardLogger()),
		WithDialOption(newBufServer(t, &testService{})),
	)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	_, err = testgrpc.NewTestServiceClient(conn).EmptyCall(rpcContext(t), &testgrpc.Empty{})
	require.NoError(t, err)

	_, err = New("localhost:1", WithCredentialsBundle(testBundle{}), WithInsecure())
	require.ErrorIs(t, err, ErrConflictingTransportSecurity)

	_, err = New("localhost:1", WithCredentialsBundle(testBundle{}), WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	require.ErrorIs(t, err, ErrConflictingTransportSecurity)
}

func TestNew_errors(t *testing.T) {
	t.Parallel()

	_, err := New("localhost:1", nil)
	require.Error(t, err)

	_, err = New("localhost:1", WithComponent(""))
	require.Error(t, err)

	_, err = New("localhost:1", WithInsecure(), WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	require.ErrorIs(t, err, ErrConflictingTransportSecurity)

	_, err = New("dns:///%zz")
	require.ErrorIs(t, err, ErrNewClient)

	_, err = New("localhost:1", WithInsecure(), WithPerRPCCredentials(testCreds{secure: true}))
	require.ErrorIs(t, err, ErrNewClient, "secure per-RPC credentials over plaintext")

	conn, err := New("localhost:1", WithInsecure(), WithPerRPCCredentials(testCreds{}))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestNew_defaultTLS(t *testing.T) {
	t.Parallel()

	t.Run("plaintext server rejected", func(t *testing.T) {
		t.Parallel()

		dialer := newBufServer(t, &testService{})

		conn, err := New(bufTarget, WithLogger(discardLogger()), WithDialOption(dialer))
		require.NoError(t, err)

		t.Cleanup(func() { _ = conn.Close() })

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		_, err = testgrpc.NewTestServiceClient(conn).UnaryCall(ctx, &testgrpc.SimpleRequest{})
		require.Error(t, err)
		require.Contains(t, []codes.Code{codes.Unavailable, codes.DeadlineExceeded}, status.Code(err))
	})

	pool, cert := makeTestCert(t)

	serverCreds := grpc.Creds(credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}))

	t.Run("untrusted certificate rejected", func(t *testing.T) {
		t.Parallel()

		dialer := newBufServer(t, &testService{}, serverCreds)

		conn, err := New("passthrough:///localhost", WithLogger(discardLogger()), WithDialOption(dialer))
		require.NoError(t, err)

		t.Cleanup(func() { _ = conn.Close() })

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		_, err = testgrpc.NewTestServiceClient(conn).UnaryCall(ctx, &testgrpc.SimpleRequest{})
		require.Error(t, err)
	})

	t.Run("custom TLS config", func(t *testing.T) {
		t.Parallel()

		dialer := newBufServer(t, &testService{}, serverCreds)

		conn, err := New(
			"passthrough:///localhost",
			WithLogger(discardLogger()),
			WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}),
			WithDialOption(dialer),
		)
		require.NoError(t, err)

		t.Cleanup(func() { _ = conn.Close() })

		_, err = testgrpc.NewTestServiceClient(conn).UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
	})

	t.Run("secure per-RPC credentials over TLS", func(t *testing.T) {
		t.Parallel()

		got := make(chan []string, 1)

		dialer := newBufServer(t, &testService{}, serverCreds, grpc.UnaryInterceptor(
			func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				md, _ := metadata.FromIncomingContext(ctx)
				got <- md.Get("authorization")

				return handler(ctx, req)
			},
		))

		conn, err := New(
			"passthrough:///localhost",
			WithLogger(discardLogger()),
			WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}),
			WithPerRPCCredentials(testCreds{secure: true}),
			WithDialOption(dialer),
		)
		require.NoError(t, err)

		t.Cleanup(func() { _ = conn.Close() })

		_, err = testgrpc.NewTestServiceClient(conn).UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
		require.NoError(t, err)
		require.Equal(t, []string{"Bearer test"}, <-got)
	})
}

func TestNew_dialOptionOverridesCredentials(t *testing.T) {
	t.Parallel()

	dialer := newBufServer(t, &testService{})

	conn, err := New(
		bufTarget,
		WithLogger(discardLogger()),
		WithDialOption(dialer, grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	_, err = testgrpc.NewTestServiceClient(conn).UnaryCall(rpcContext(t), &testgrpc.SimpleRequest{})
	require.NoError(t, err)
}

func TestNew_interceptorOrder(t *testing.T) {
	t.Parallel()

	var order []string

	mark := func(name string) grpc.UnaryClientInterceptor {
		return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			_, hasDeadline := ctx.Deadline()
			if !hasDeadline {
				name += "-nodeadline"
			}

			order = append(order, name)

			return invoker(ctx, method, req, reply, cc, opts...)
		}
	}

	client := newTestClient(t, &testService{}, WithUnaryInterceptor(mark("a"), mark("b")))

	_, err := client.UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, order, "custom interceptors run inside the default deadline")
}
