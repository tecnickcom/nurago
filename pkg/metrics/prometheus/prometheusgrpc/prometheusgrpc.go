/*
Package prometheusgrpc collects Prometheus metrics for gRPC servers and
clients.

[Metrics] wraps the server and client metrics of
github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus:
  - [Metrics.ServerOptions] and [Metrics.DialOptions] return the unary and
    stream interceptors that record the metrics. They satisfy the
    grpcutil.ServerInstrumenter and grpcutil.ClientInstrumenter interfaces,
    so a Metrics value is passed to grpcserver.WithInstrumenter and
    grpcclient.WithInstrumenter.
  - [Metrics.InitializeServer] creates the series of every registered method
    that passes the filters at zero before the first call. It satisfies
    grpcutil.ServerInitializer, so grpcserver calls it once the services are
    registered.
  - [Metrics.Describe] and [Metrics.Collect] implement prometheus.Collector,
    so a Metrics value is registered with the WithCollector option of
    [github.com/tecnickcom/nurago/pkg/metrics/prometheus], next to the HTTP
    metrics, or with any other Prometheus registry.

# Metrics

The upstream metric names are kept, so existing dashboards and alerts work:
  - grpc_server_started_total, grpc_server_handled_total,
    grpc_server_msg_received_total, grpc_server_msg_sent_total
  - grpc_client_started_total, grpc_client_handled_total,
    grpc_client_msg_received_total, grpc_client_msg_sent_total
  - grpc_server_handling_seconds and grpc_client_handling_seconds histograms

Every series is labeled with grpc_type, grpc_service, and grpc_method, and
the handled counters also with grpc_code. The handling time histograms are
enabled by default, with the buckets of the metrics/prometheus HTTP duration
histograms (0.001 to 100 seconds). [WithoutHandlingTimeHistogram] disables
them.

# Filtering

By default the RPCs of the grpc.health.v1.Health service and of the
grpc.reflection.* services are not instrumented, and InitializeServer creates
no series for them, so health probes and reflection clients do not skew the
metrics. [WithFilter] adds more filters, and [WithoutDefaultFilter] removes
the default one.

# Exemplars

[WithExemplarFromContext] attaches exemplar labels to every observation.
[TraceIDExemplar] returns the trace ID stored in the context by grpcserver
and grpcclient, so a metric sample can be matched with the RPC log entries.
Exemplars are exposed only in the OpenMetrics format: enable it with the
EnableOpenMetrics field of the promhttp.HandlerOpts set with the
WithHandlerOpts option of the metrics/prometheus package.

# When To Use

  - A service scraped by Prometheus serves or calls gRPC APIs.
  - gRPC and HTTP metrics must be exposed from the same registry and endpoint.
*/
package prometheusgrpc

import (
	"context"
	"slices"
	"strings"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"google.golang.org/grpc"
)

// ExemplarLabel is the exemplar label set by [TraceIDExemplar]. It matches the
// trace ID log field.
const ExemplarLabel = traceid.DefaultLogKey

// Metrics collects the Prometheus metrics of gRPC servers and clients.
//
// Create it with [New].
type Metrics struct {
	filters      []Filter
	server       *grpcprom.ServerMetrics
	client       *grpcprom.ClientMetrics
	unaryServer  grpc.UnaryServerInterceptor
	streamServer grpc.StreamServerInterceptor
	unaryClient  grpc.UnaryClientInterceptor
	streamClient grpc.StreamClientInterceptor
}

// New returns a Metrics value configured with opts. It returns an error when
// an option is invalid.
func New(opts ...Option) (*Metrics, error) {
	cfg := defaultConfig()

	for _, applyOpt := range opts {
		if applyOpt == nil {
			return nil, errNilOption
		}

		err := applyOpt(cfg)
		if err != nil {
			return nil, err
		}
	}

	server := grpcprom.NewServerMetrics(cfg.serverMetricsOptions()...)
	client := grpcprom.NewClientMetrics(cfg.clientMetricsOptions()...)
	iopts := cfg.interceptorOptions()

	m := &Metrics{
		filters: cfg.rpcFilters(),
		server:  server,
		client:  client,
	}

	m.unaryServer = m.unaryServerInterceptor(server.UnaryServerInterceptor(iopts...))
	m.streamServer = m.streamServerInterceptor(server.StreamServerInterceptor(iopts...))
	m.unaryClient = m.unaryClientInterceptor(client.UnaryClientInterceptor(iopts...))
	m.streamClient = m.streamClientInterceptor(client.StreamClientInterceptor(iopts...))

	return m, nil
}

// ServerOptions returns the server options that add the server metrics
// interceptors.
func (m *Metrics) ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(m.unaryServer),
		grpc.ChainStreamInterceptor(m.streamServer),
	}
}

// DialOptions returns the dial options that add the client metrics
// interceptors.
func (m *Metrics) DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(m.unaryClient),
		grpc.WithChainStreamInterceptor(m.streamClient),
	}
}

// InitializeServer creates at zero the server series of every method
// registered on s that passes the filters: the started and message counters,
// the handled counter for every code, and the handling time histogram when it
// is enabled. Call it after the services are registered.
func (m *Metrics) InitializeServer(s *grpc.Server) {
	m.server.InitializeMetrics(filteredServices{s: s, m: m})
}

// Describe implements prometheus.Collector.
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	m.server.Describe(ch)
	m.client.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	m.server.Collect(ch)
	m.client.Collect(ch)
}

// DefaultFilter returns the filter applied unless [WithoutDefaultFilter] is
// set. It excludes the RPCs of the grpc.health.v1.Health service and of the
// services whose name starts with "grpc.reflection.".
func DefaultFilter() Filter {
	return func(fullMethod string) bool {
		service, _, _ := grpcutil.ParseFullMethod(fullMethod)

		return service != "grpc.health.v1.Health" && !strings.HasPrefix(service, "grpc.reflection.")
	}
}

// instrumented reports whether every filter accepts fullMethod.
func (m *Metrics) instrumented(fullMethod string) bool {
	for _, f := range m.filters {
		if !f(fullMethod) {
			return false
		}
	}

	return true
}

// unaryServerInterceptor returns next for the RPCs that pass the filters.
func (m *Metrics) unaryServerInterceptor(next grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !m.instrumented(info.FullMethod) {
			return handler(ctx, req)
		}

		return next(ctx, req, info, handler)
	}
}

// streamServerInterceptor returns next for the RPCs that pass the filters.
func (m *Metrics) streamServerInterceptor(next grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !m.instrumented(info.FullMethod) {
			return handler(srv, ss)
		}

		return next(srv, ss, info, handler)
	}
}

// unaryClientInterceptor returns next for the RPCs that pass the filters.
func (m *Metrics) unaryClientInterceptor(next grpc.UnaryClientInterceptor) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if !m.instrumented(method) {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		return next(ctx, method, req, reply, cc, invoker, opts...)
	}
}

// streamClientInterceptor returns next for the RPCs that pass the filters.
func (m *Metrics) streamClientInterceptor(next grpc.StreamClientInterceptor) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		if !m.instrumented(method) {
			return streamer(ctx, desc, cc, method, opts...)
		}

		return next(ctx, desc, cc, method, streamer, opts...)
	}
}

// filteredServices exposes the services of s with only the methods that pass
// the filters of m.
type filteredServices struct {
	s *grpc.Server
	m *Metrics
}

// GetServiceInfo implements reflection.ServiceInfoProvider.
func (f filteredServices) GetServiceInfo() map[string]grpc.ServiceInfo {
	out := make(map[string]grpc.ServiceInfo)

	for name, info := range f.s.GetServiceInfo() {
		info.Methods = slices.DeleteFunc(slices.Clone(info.Methods), func(mi grpc.MethodInfo) bool {
			return !f.m.instrumented("/" + name + "/" + mi.Name)
		})

		out[name] = info
	}

	return out
}

// TraceIDExemplar returns the trace ID stored in ctx with the traceid package
// as an exemplar label set with the [ExemplarLabel] key. It returns nil, so no
// exemplar is attached, when ctx carries no valid trace ID.
func TraceIDExemplar(ctx context.Context) prometheus.Labels {
	id := traceid.FromContext(ctx, "")
	if !traceid.Valid(id) {
		return nil
	}

	return prometheus.Labels{ExemplarLabel: id}
}
