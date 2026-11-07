package opentelgrpc_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/tecnickcom/nurago/pkg/grpcclient"
	"github.com/tecnickcom/nurago/pkg/grpcserver"
	"github.com/tecnickcom/nurago/pkg/metrics/opentel/opentelgrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
)

// exampleBinder registers the service implementations.
type exampleBinder struct{}

func (exampleBinder) BindGRPC(_ context.Context, s grpc.ServiceRegistrar) error {
	// Register generated services here, e.g. pb.RegisterGreeterServer(s, &greeter{}).
	testgrpc.RegisterTestServiceServer(s, testService{})
	return nil
}

func ExampleNew() {
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	// Without WithTracerProvider, WithMeterProvider, and WithPropagators the
	// global providers installed by metrics/opentel.New are used.
	spans := tracetest.NewSpanRecorder()

	in, err := opentelgrpc.New(
		opentelgrpc.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	srv, err := grpcserver.New(
		ctx,
		exampleBinder{},
		grpcserver.WithServerAddr("127.0.0.1:0"),
		grpcserver.WithLogger(logger),
		grpcserver.WithHealthService(nil),
		grpcserver.WithInstrumenter(in),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	srv.StartServer()

	conn, err := grpcclient.New(
		srv.Addr().String(),
		grpcclient.WithInsecure(),
		grpcclient.WithLogger(logger),
		grpcclient.WithInstrumenter(in),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	defer func() { _ = conn.Close() }()

	_, err = testgrpc.NewTestServiceClient(conn).UnaryCall(ctx, &testgrpc.SimpleRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	// Health checks are not traced by default.
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	// A server span ends after the response is sent. Shutdown waits for the
	// running RPCs, so every span has ended once it returns.
	err = srv.Shutdown(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}

	var names []string
	for _, s := range spans.Ended() {
		names = append(names, s.SpanKind().String()+" "+s.Name())
	}

	slices.Sort(names)

	for _, n := range names {
		fmt.Println(n)
	}

	// Output:
	// client grpc.testing.TestService/UnaryCall
	// server grpc.testing.TestService/UnaryCall
}
