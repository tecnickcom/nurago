package opentelgrpc_test

import (
	"context"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/metrics/opentel/opentelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/test/bufconn"
)

var (
	_ grpcutil.ServerInstrumenter = (*opentelgrpc.Instrumenter)(nil)
	_ grpcutil.ClientInstrumenter = (*opentelgrpc.Instrumenter)(nil)
)

// testService answers UnaryCall with the span context seen by the handler in
// the Username field.
type testService struct {
	testgrpc.UnimplementedTestServiceServer
}

func (testService) UnaryCall(ctx context.Context, _ *testgrpc.SimpleRequest) (*testgrpc.SimpleResponse, error) {
	return &testgrpc.SimpleResponse{Username: trace.SpanContextFromContext(ctx).TraceID().String()}, nil
}

// telemetry holds in-memory span and metric collectors.
type telemetry struct {
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
}

// providerOptions returns the options that route the spans and metrics to t,
// with W3C trace context propagation.
func (tm *telemetry) providerOptions() []opentelgrpc.Option {
	return []opentelgrpc.Option{
		opentelgrpc.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(tm.spans))),
		opentelgrpc.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(tm.reader))),
		opentelgrpc.WithPropagators(propagation.TraceContext{}),
	}
}

// spanNames returns the names of the ended spans.
func (tm *telemetry) spanNames() []string {
	ended := tm.spans.Ended()

	names := make([]string, 0, len(ended))
	for _, s := range ended {
		names = append(names, s.Name())
	}

	return names
}

// metricNames returns the names of the collected metrics.
func (tm *telemetry) metricNames(t *testing.T) []string {
	t.Helper()

	var rm metricdata.ResourceMetrics

	require.NoError(t, tm.reader.Collect(t.Context(), &rm))

	var names []string

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names = append(names, m.Name)
		}
	}

	return names
}

// waitSpans waits until n spans have ended.
func (tm *telemetry) waitSpans(t *testing.T, n int) {
	t.Helper()

	// A server span ends after the response is sent.
	require.Eventually(t, func() bool { return len(tm.spans.Ended()) >= n }, 5*time.Second, 5*time.Millisecond)
}

// newTelemetry returns empty in-memory collectors.
func newTelemetry() *telemetry {
	return &telemetry{spans: tracetest.NewSpanRecorder(), reader: sdkmetric.NewManualReader()}
}

// newTestConn serves the test, health, and reflection services on an
// in-memory listener with the server options of in and returns a connection
// using its dial options.
func newTestConn(t *testing.T, in *opentelgrpc.Instrumenter) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1 << 20)

	srv := grpc.NewServer(in.ServerOptions()...)
	testgrpc.RegisterTestServiceServer(srv, testService{})
	healthpb.RegisterHealthServer(srv, health.NewServer())
	reflection.Register(srv)

	go func() { _ = srv.Serve(lis) }()

	t.Cleanup(srv.Stop)

	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	}, in.DialOptions()...)

	conn, err := grpc.NewClient("passthrough:///bufnet", dialOpts...)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// callHealthAndReflection calls the health and reflection services.
func callHealthAndReflection(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()

	_, err := healthpb.NewHealthClient(conn).Check(t.Context(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)

	stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(t.Context())
	require.NoError(t, err)

	require.NoError(t, stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{},
	}))

	_, err = stream.Recv()
	require.NoError(t, err)

	require.NoError(t, stream.CloseSend())

	_, err = stream.Recv()
	require.Error(t, err)
}

func TestNew(t *testing.T) {
	t.Parallel()

	_, err := opentelgrpc.New(nil)
	require.Error(t, err)

	_, err = opentelgrpc.New(opentelgrpc.WithFilter(nil))
	require.Error(t, err)

	in, err := opentelgrpc.New()
	require.NoError(t, err)
	require.Len(t, in.ServerOptions(), 1)
	require.Len(t, in.DialOptions(), 1)
}

func TestInstrumenter_spansAndMetrics(t *testing.T) {
	t.Parallel()

	tm := newTelemetry()

	in, err := opentelgrpc.New(tm.providerOptions()...)
	require.NoError(t, err)

	conn := newTestConn(t, in)

	resp, err := testgrpc.NewTestServiceClient(conn).UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	tm.waitSpans(t, 2)

	spans := tm.spans.Ended()
	require.Len(t, spans, 2, "one client and one server span")

	byKind := map[trace.SpanKind]sdktrace.ReadOnlySpan{}

	for _, s := range spans {
		require.Equal(t, "grpc.testing.TestService/UnaryCall", s.Name())
		byKind[s.SpanKind()] = s
	}

	client, server := byKind[trace.SpanKindClient], byKind[trace.SpanKindServer]
	require.NotNil(t, client)
	require.NotNil(t, server)

	// The trace context propagates from the client to the server.
	require.Equal(t, client.SpanContext().TraceID(), server.SpanContext().TraceID())
	require.Equal(t, client.SpanContext().SpanID(), server.Parent().SpanID())
	require.Equal(t, client.SpanContext().TraceID().String(), resp.GetUsername())

	// The server metrics are recorded after the server span ends.
	for _, prefix := range []string{"rpc.server.", "rpc.client."} {
		require.Eventually(t, func() bool {
			return slices.ContainsFunc(tm.metricNames(t), func(name string) bool {
				return strings.HasPrefix(name, prefix)
			})
		}, 5*time.Second, 5*time.Millisecond, "%s* metrics are recorded", prefix)
	}
}

func TestInstrumenter_defaultFilter(t *testing.T) {
	t.Parallel()

	tm := newTelemetry()

	in, err := opentelgrpc.New(tm.providerOptions()...)
	require.NoError(t, err)

	callHealthAndReflection(t, newTestConn(t, in))

	require.Empty(t, tm.spanNames(), "health and reflection RPCs produce no spans")
	require.Empty(t, tm.metricNames(t), "health and reflection RPCs produce no metrics")
}

func TestInstrumenter_withoutDefaultFilter(t *testing.T) {
	t.Parallel()

	tm := newTelemetry()

	in, err := opentelgrpc.New(append(tm.providerOptions(), opentelgrpc.WithoutDefaultFilter())...)
	require.NoError(t, err)

	callHealthAndReflection(t, newTestConn(t, in))

	tm.waitSpans(t, 4)

	require.ElementsMatch(t, []string{
		"grpc.health.v1.Health/Check",
		"grpc.health.v1.Health/Check",
		"grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		"grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
	}, tm.spanNames())
}

func TestInstrumenter_withFilter(t *testing.T) {
	t.Parallel()

	tm := newTelemetry()

	in, err := opentelgrpc.New(append(tm.providerOptions(),
		opentelgrpc.WithFilter(filters.Not(filters.MethodName("EmptyCall"))),
		opentelgrpc.WithOtelGRPCOptions(otelgrpc.WithSpanAttributes()),
	)...)
	require.NoError(t, err)

	conn := newTestConn(t, in)
	client := testgrpc.NewTestServiceClient(conn)

	_, err = client.UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	_, err = client.EmptyCall(t.Context(), &testgrpc.Empty{})
	require.Error(t, err, "EmptyCall is not implemented")

	callHealthAndReflection(t, conn)

	tm.waitSpans(t, 2)

	require.Equal(t, []string{
		"grpc.testing.TestService/UnaryCall",
		"grpc.testing.TestService/UnaryCall",
	}, tm.spanNames(), "the custom filter is combined with the default one")
}

func TestDefaultFilter(t *testing.T) {
	t.Parallel()

	f := opentelgrpc.DefaultFilter()

	for method, want := range map[string]bool{
		"/grpc.testing.TestService/UnaryCall":                            true,
		"/grpc.health.v1.Health/Check":                                   false,
		"/grpc.health.v1.Health/Watch":                                   false,
		"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo":      false,
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo": false,
		"/grpc.healthcheck.Custom/Check":                                 true,
	} {
		require.Equal(t, want, f(&stats.RPCTagInfo{FullMethodName: method}), method)
	}
}
