package grpcclient_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcclient"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func ExampleNew() {
	ctx := context.Background()

	// A local server to call. In production the target is a remote address
	// such as "dns:///items.internal:8019".
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)
		return
	}

	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())

	go func() { _ = srv.Serve(lis) }()

	defer srv.Stop()

	// Create the connection once and share it: it is safe for concurrent use.
	// TLS is the default; the local server is plaintext.
	conn, err := grpcclient.New(
		lis.Addr().String(),
		grpcclient.WithInsecure(),
		grpcclient.WithTimeout(5*time.Second),
		grpcclient.WithComponent("example"),
		grpcclient.WithLogger(slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	defer func() { _ = conn.Close() }()

	// The trace ID in the context is sent in the "x-request-id" metadata.
	ctx = traceid.NewContext(ctx, "019a1d4e-7b3c-7f00-8000-000000000001")

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(resp.GetStatus())

	// Output:
	// SERVING
}

// flakyHealth fails the first Check calls with codes.Unavailable.
type flakyHealth struct {
	healthpb.UnimplementedHealthServer

	calls atomic.Int32
}

func (h *flakyHealth) Check(_ context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if h.calls.Add(1) < 3 {
		return nil, status.Error(codes.Unavailable, "warming up") //nolint:wrapcheck
	}

	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func ExampleWithRetryPolicy() {
	ctx := context.Background()

	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)
		return
	}

	svc := &flakyHealth{}

	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, svc)

	go func() { _ = srv.Serve(lis) }()

	defer srv.Stop()

	conn, err := grpcclient.New(
		lis.Addr().String(),
		grpcclient.WithInsecure(),
		grpcclient.WithLogger(slog.New(slog.DiscardHandler)),
		// Retry only the idempotent Check method.
		grpcclient.WithRetryPolicy(grpcclient.RetryPolicy{
			Methods:           []string{healthpb.Health_Check_FullMethodName},
			MaxAttempts:       4,
			InitialBackoff:    10 * time.Millisecond,
			MaxBackoff:        100 * time.Millisecond,
			BackoffMultiplier: 2,
			RetryableCodes:    []codes.Code{codes.Unavailable},
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	defer func() { _ = conn.Close() }()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(resp.GetStatus(), "after", svc.calls.Load(), "attempts")

	// Output:
	// SERVING after 3 attempts
}
