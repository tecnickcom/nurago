package grpcserver_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tecnickcom/nurago/pkg/grpcserver"
	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// exampleBinder registers the service implementations.
type exampleBinder struct{}

func (b *exampleBinder) BindGRPC(_ context.Context, s grpc.ServiceRegistrar) error {
	// Register generated services here, e.g. pb.RegisterGreeterServer(s, &greeter{}).
	healthpb.RegisterHealthServer(s, health.NewServer())
	return nil
}

func ExampleNew() {
	ctx := context.Background()

	srv, err := grpcserver.New(
		ctx,
		&exampleBinder{},
		grpcserver.WithServerAddr("127.0.0.1:0"), // ephemeral port; see srv.Addr() for the actual address
		grpcserver.WithRequestTimeout(30*time.Second),
		grpcserver.WithShutdownTimeout(5*time.Second),
		grpcserver.WithLogger(slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	// The server runs in the background; canceling ctx or calling Shutdown
	// stops it gracefully.
	srv.StartServer()

	conn, err := grpc.NewClient(srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	fmt.Println(resp.GetStatus())

	err = srv.Shutdown(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("server stopped")

	// Output:
	// SERVING
	// server stopped
}

func ExampleWithHealthService() {
	ctx := context.Background()

	checks := []healthrun.HealthCheck{
		healthrun.New("database", healthrun.HealthCheckFunc(func(_ context.Context) error {
			return nil
		})),
		healthrun.New("cache", healthrun.HealthCheckFunc(func(_ context.Context) error {
			return errors.New("connection refused")
		})),
	}

	srv, err := grpcserver.New(
		ctx,
		grpcserver.NopBinder(),
		grpcserver.WithServerAddr("127.0.0.1:0"),
		grpcserver.WithLogger(slog.New(slog.DiscardHandler)),
		grpcserver.WithHealthService(checks, grpcserver.WithHealthCheckTimeout(time.Second)),
		grpcserver.WithoutMethodLogger(healthpb.Health_Check_FullMethodName),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	srv.StartServer()

	defer func() { _ = srv.Shutdown(ctx) }()

	conn, err := grpc.NewClient(srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Println(err)
		return
	}

	defer func() { _ = conn.Close() }()

	client := healthpb.NewHealthClient(conn)

	// The empty service name reports the overall status; each check ID is also
	// a service name.
	for _, service := range []string{"", "database", "cache"} {
		resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Printf("%q: %v\n", service, resp.GetStatus())
	}

	// Output:
	// "": NOT_SERVING
	// "database": SERVING
	// "cache": NOT_SERVING
}
