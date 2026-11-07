package prometheusgrpc_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/tecnickcom/nurago/pkg/grpcclient"
	"github.com/tecnickcom/nurago/pkg/grpcserver"
	"github.com/tecnickcom/nurago/pkg/metrics/prometheus"
	"github.com/tecnickcom/nurago/pkg/metrics/prometheus/prometheusgrpc"
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

	m, err := prometheusgrpc.New(prometheusgrpc.WithExemplarFromContext(prometheusgrpc.TraceIDExemplar))
	if err != nil {
		fmt.Println(err)
		return
	}

	// The gRPC metrics are exposed by the same endpoint as the HTTP metrics.
	metricsClient, err := prometheus.New(prometheus.WithCollector(m))
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
		grpcserver.WithInstrumenter(m),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	srv.StartServer()

	defer func() { _ = srv.Shutdown(ctx) }()

	conn, err := grpcclient.New(
		srv.Addr().String(),
		grpcclient.WithInsecure(),
		grpcclient.WithLogger(logger),
		grpcclient.WithInstrumenter(m),
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

	// Health checks are not instrumented by default.
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	rec := httptest.NewRecorder()
	metricsClient.MetricsHandlerFunc()(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", nil))

	for line := range strings.SplitSeq(rec.Body.String(), "\n") {
		if strings.Contains(line, `grpc_code="OK"`) && !strings.HasSuffix(line, " 0") {
			fmt.Println(line)
		}
	}

	// Output:
	// grpc_client_handled_total{grpc_code="OK",grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 1
	// grpc_server_handled_total{grpc_code="OK",grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 1
}
