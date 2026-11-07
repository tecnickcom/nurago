package prometheusgrpc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	nprom "github.com/tecnickcom/nurago/pkg/metrics/prometheus"
	"github.com/tecnickcom/nurago/pkg/metrics/prometheus/prometheusgrpc"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

var (
	_ grpcutil.ServerInstrumenter = (*prometheusgrpc.Metrics)(nil)
	_ grpcutil.ClientInstrumenter = (*prometheusgrpc.Metrics)(nil)
	_ grpcutil.ServerInitializer  = (*prometheusgrpc.Metrics)(nil)
	_ prometheus.Collector        = (*prometheusgrpc.Metrics)(nil)
)

// testService answers UnaryCall with the requested status and
// StreamingOutputCall with one message per response parameter.
type testService struct {
	testgrpc.UnimplementedTestServiceServer
}

func (testService) UnaryCall(_ context.Context, req *testgrpc.SimpleRequest) (*testgrpc.SimpleResponse, error) {
	if c := req.GetResponseStatus().GetCode(); c != 0 {
		return nil, status.Error(codes.Code(c), "test error") //nolint:wrapcheck
	}

	return &testgrpc.SimpleResponse{}, nil
}

//nolint:wrapcheck
func (testService) StreamingOutputCall(
	req *testgrpc.StreamingOutputCallRequest,
	stream grpc.ServerStreamingServer[testgrpc.StreamingOutputCallResponse],
) error {
	for range req.GetResponseParameters() {
		err := stream.Send(&testgrpc.StreamingOutputCallResponse{})
		if err != nil {
			return err
		}
	}

	return nil
}

// traceIDInterceptor stores the trace ID of the incoming metadata in the
// context, as grpcserver does before the instrumenter interceptors run.
func traceIDInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	id := grpcutil.TraceIDFromIncomingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, "")

	return handler(traceid.NewContext(ctx, id), req)
}

// newTestClient serves the test service on an in-memory listener with the
// server options of m and returns a client using the dial options of m.
func newTestClient(t *testing.T, m *prometheusgrpc.Metrics) testgrpc.TestServiceClient {
	t.Helper()

	lis := bufconn.Listen(1 << 20)

	opts := append([]grpc.ServerOption{grpc.ChainUnaryInterceptor(traceIDInterceptor)}, m.ServerOptions()...)
	srv := grpc.NewServer(opts...)
	testgrpc.RegisterTestServiceServer(srv, testService{})
	m.InitializeServer(srv)

	go func() { _ = srv.Serve(lis) }()

	t.Cleanup(srv.Stop)

	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	}, m.DialOptions()...)

	conn, err := grpc.NewClient("passthrough:///bufnet", dialOpts...)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return testgrpc.NewTestServiceClient(conn)
}

// runRPCs makes a successful and a failing unary call, and a server stream
// that sends two messages.
func runRPCs(t *testing.T, ctx context.Context, client testgrpc.TestServiceClient) {
	t.Helper()

	_, err := client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	_, err = client.UnaryCall(ctx, &testgrpc.SimpleRequest{ResponseStatus: &testgrpc.EchoStatus{Code: int32(codes.NotFound)}})
	require.Equal(t, codes.NotFound, status.Code(err))

	stream, err := client.StreamingOutputCall(ctx, &testgrpc.StreamingOutputCallRequest{
		ResponseParameters: []*testgrpc.ResponseParameters{{}, {}},
	})
	require.NoError(t, err)

	for {
		_, err = stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)
	}
}

// scrape returns the text exposition of the metrics/prometheus client.
func scrape(t *testing.T, c *nprom.Client) string {
	t.Helper()

	rec := httptest.NewRecorder()
	c.MetricsHandlerFunc()(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	return rec.Body.String()
}

// gatherFamily returns the metric family name gathered from reg.
func gatherFamily(t *testing.T, reg prometheus.Gatherer, name string) *dto.MetricFamily {
	t.Helper()

	mfs, err := reg.Gather()
	require.NoError(t, err)

	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}

	return nil
}

// labelValue returns the value of the label name of m.
func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}

	return ""
}

func TestNew_errors(t *testing.T) {
	t.Parallel()

	_, err := prometheusgrpc.New(nil)
	require.Error(t, err)

	_, err = prometheusgrpc.New(prometheusgrpc.WithServerHandlingTimeBuckets([]float64{2, 1}))
	require.Error(t, err)
}

func TestMetrics_scrape(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New()
	require.NoError(t, err)

	c, err := nprom.New(nprom.WithCollector(m))
	require.NoError(t, err)

	client := newTestClient(t, m)

	body := scrape(t, c)

	for _, want := range []string{
		`grpc_server_started_total{grpc_method="StreamingInputCall",grpc_service="grpc.testing.TestService",grpc_type="client_stream"} 0`,
		`grpc_server_handled_total{grpc_code="Unavailable",grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 0`,
		`grpc_server_handling_seconds_count{grpc_method="FullDuplexCall",grpc_service="grpc.testing.TestService",grpc_type="bidi_stream"} 0`,
	} {
		require.Contains(t, body, want, "InitializeServer creates the series at zero")
	}

	require.NotContains(t, body, "grpc_client_started_total{", "client series exist only after a call")

	runRPCs(t, t.Context(), client)

	body = scrape(t, c)

	for _, want := range []string{
		`grpc_server_handled_total{grpc_code="OK",grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 1`,
		`grpc_server_handled_total{grpc_code="NotFound",grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 1`,
		`grpc_server_msg_sent_total{grpc_method="StreamingOutputCall",grpc_service="grpc.testing.TestService",grpc_type="server_stream"} 2`,
		`grpc_server_handling_seconds_count{grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 2`,
		`grpc_server_handling_seconds_bucket{grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary",le="0.001"}`,
		`grpc_server_handling_seconds_bucket{grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary",le="100"}`,
		`grpc_server_handling_seconds_bucket{grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary",le="+Inf"} 2`,
		`grpc_client_started_total{grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 2`,
		`grpc_client_handled_total{grpc_code="NotFound",grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary"} 1`,
		`grpc_client_msg_received_total{grpc_method="StreamingOutputCall",grpc_service="grpc.testing.TestService",grpc_type="server_stream"} 2`,
		`grpc_client_handling_seconds_bucket{grpc_method="UnaryCall",grpc_service="grpc.testing.TestService",grpc_type="unary",le="10"} 2`,
		"go_goroutines ", // the default collectors share the registry
	} {
		require.Contains(t, body, want)
	}
}

func TestMetrics_buckets(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New(
		prometheusgrpc.WithServerHandlingTimeBuckets([]float64{0.5, 2}),
		prometheusgrpc.WithClientHandlingTimeBuckets([]float64{0.25}),
	)
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(m))

	runRPCs(t, t.Context(), newTestClient(t, m))

	bounds := func(name string) []float64 {
		mf := gatherFamily(t, reg, name)
		require.NotNil(t, mf, name)

		buckets := mf.GetMetric()[0].GetHistogram().GetBucket()

		out := make([]float64, 0, len(buckets))
		for _, b := range buckets {
			out = append(out, b.GetUpperBound())
		}

		return out
	}

	require.InDeltaSlice(t, []float64{0.5, 2}, bounds("grpc_server_handling_seconds"), 1e-9)
	require.InDeltaSlice(t, []float64{0.25}, bounds("grpc_client_handling_seconds"), 1e-9)
}

func TestMetrics_withoutHandlingTimeHistogram(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New(prometheusgrpc.WithoutHandlingTimeHistogram())
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(m))

	runRPCs(t, t.Context(), newTestClient(t, m))

	require.Nil(t, gatherFamily(t, reg, "grpc_server_handling_seconds"))
	require.Nil(t, gatherFamily(t, reg, "grpc_client_handling_seconds"))
	require.NotNil(t, gatherFamily(t, reg, "grpc_server_handled_total"))
}

func TestMetrics_exemplar(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New(prometheusgrpc.WithExemplarFromContext(prometheusgrpc.TraceIDExemplar))
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(m))

	client := newTestClient(t, m)

	const id = "019a1d4e-7b3c-7f00-8000-000000000001"

	// The client interceptor reads the context value, the server one the
	// value stored by traceIDInterceptor from the metadata.
	ctx := traceid.NewContext(t.Context(), id)
	ctx = metadata.AppendToOutgoingContext(ctx, grpcutil.DefaultTraceIDMetadataKey, id)

	_, err = client.UnaryCall(ctx, &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	for _, name := range []string{"grpc_server_handled_total", "grpc_client_handled_total"} {
		mf := gatherFamily(t, reg, name)
		require.NotNil(t, mf, name)

		var found bool

		for _, metric := range mf.GetMetric() {
			if labelValue(metric, "grpc_code") != "OK" || labelValue(metric, "grpc_method") != "UnaryCall" {
				continue
			}

			ex := metric.GetCounter().GetExemplar()
			require.NotNil(t, ex, name)
			require.Equal(t, prometheusgrpc.ExemplarLabel, ex.GetLabel()[0].GetName())
			require.Equal(t, id, ex.GetLabel()[0].GetValue())

			found = true
		}

		require.True(t, found, name)
	}
}

func TestMetrics_contextLabels(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New(
		prometheusgrpc.WithServerMetricsOptions(grpcprom.WithContextLabels("tenant")),
		prometheusgrpc.WithClientMetricsOptions(grpcprom.WithClientContextLabels("tenant")),
		prometheusgrpc.WithInterceptorOptions(grpcprom.WithLabelsFromContext(func(context.Context) prometheus.Labels {
			return prometheus.Labels{"tenant": "acme"}
		})),
	)
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(m))

	_, err = newTestClient(t, m).UnaryCall(t.Context(), &testgrpc.SimpleRequest{})
	require.NoError(t, err)

	mf := gatherFamily(t, reg, "grpc_client_handled_total")
	require.NotNil(t, mf)
	require.Equal(t, "acme", labelValue(mf.GetMetric()[0], "tenant"))
}

func TestMetrics_contextLabelsWithoutLabelsFunc(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New(
		prometheusgrpc.WithServerMetricsOptions(grpcprom.WithContextLabels("tenant")),
		prometheusgrpc.WithClientMetricsOptions(grpcprom.WithClientContextLabels("tenant")),
	)
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(m))

	runRPCs(t, t.Context(), newTestClient(t, m))

	for _, name := range []string{"grpc_server_handled_total", "grpc_client_handled_total"} {
		mf := gatherFamily(t, reg, name)
		require.NotNil(t, mf, name)

		for _, metric := range mf.GetMetric() {
			require.Contains(t, metric.GetLabel(), &dto.LabelPair{Name: new("tenant"), Value: new("")}, name)
		}
	}
}

func TestDefaultFilter(t *testing.T) {
	t.Parallel()

	f := prometheusgrpc.DefaultFilter()

	require.False(t, f("/grpc.health.v1.Health/Check"))
	require.False(t, f("/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"))
	require.False(t, f("/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"))
	require.True(t, f("/grpc.testing.TestService/UnaryCall"))
	require.True(t, f("/grpc.healthz.Service/Check"))
	require.True(t, f("invalid"))
}

func TestMetrics_filter(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New(prometheusgrpc.WithFilter(func(fullMethod string) bool {
		return fullMethod != "/grpc.testing.TestService/UnaryCall" &&
			fullMethod != "/grpc.testing.TestService/StreamingOutputCall"
	}))
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(m))

	runRPCs(t, t.Context(), newTestClient(t, m))

	for _, name := range []string{"grpc_server_started_total", "grpc_client_started_total"} {
		mf := gatherFamily(t, reg, name)
		if mf == nil {
			continue
		}

		for _, metric := range mf.GetMetric() {
			require.NotEqual(t, "UnaryCall", labelValue(metric, "grpc_method"), name)
			require.NotEqual(t, "StreamingOutputCall", labelValue(metric, "grpc_method"), name)
		}
	}

	mf := gatherFamily(t, reg, "grpc_server_started_total")
	require.NotNil(t, mf, "the other methods are initialized")
	require.Nil(t, gatherFamily(t, reg, "grpc_client_started_total"), "the client RPCs are filtered")
}

func TestMetrics_initializeServerDefaultFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		opts       []prometheusgrpc.Option
		wantHealth bool
	}{
		{name: "default filter"},
		{name: "without default filter", opts: []prometheusgrpc.Option{prometheusgrpc.WithoutDefaultFilter()}, wantHealth: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := prometheusgrpc.New(tt.opts...)
			require.NoError(t, err)

			reg := prometheus.NewRegistry()
			require.NoError(t, reg.Register(m))

			srv := grpc.NewServer()
			healthgrpc.RegisterHealthServer(srv, health.NewServer())
			testgrpc.RegisterTestServiceServer(srv, testService{})
			m.InitializeServer(srv)

			services := map[string]bool{}

			for _, metric := range gatherFamily(t, reg, "grpc_server_started_total").GetMetric() {
				services[labelValue(metric, "grpc_service")] = true
			}

			require.True(t, services["grpc.testing.TestService"])
			require.Equal(t, tt.wantHealth, services["grpc.health.v1.Health"])
		})
	}
}

func TestTraceIDExemplar(t *testing.T) {
	t.Parallel()

	require.Nil(t, prometheusgrpc.TraceIDExemplar(t.Context()))
	require.Nil(t, prometheusgrpc.TraceIDExemplar(traceid.NewContext(t.Context(), "bad id")))
	require.Equal(t,
		prometheus.Labels{prometheusgrpc.ExemplarLabel: "abc-123"},
		prometheusgrpc.TraceIDExemplar(traceid.NewContext(t.Context(), "abc-123")),
	)
}

func TestMetrics_describe(t *testing.T) {
	t.Parallel()

	m, err := prometheusgrpc.New()
	require.NoError(t, err)

	ch := make(chan *prometheus.Desc, 64)
	m.Describe(ch)
	close(ch)

	var names []string
	for d := range ch {
		names = append(names, d.String())
	}

	joined := strings.Join(names, "\n")

	require.Contains(t, joined, `"grpc_server_handled_total"`)
	require.Contains(t, joined, `"grpc_client_handling_seconds"`)
}
