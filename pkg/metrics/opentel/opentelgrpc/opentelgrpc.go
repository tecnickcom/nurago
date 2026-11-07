/*
Package opentelgrpc adds OpenTelemetry tracing and metrics to gRPC servers and
clients.

[Instrumenter] wraps the stats handlers of
go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc:
  - [Instrumenter.ServerOptions] returns a grpc.StatsHandler option with the
    otelgrpc server handler, and [Instrumenter.DialOptions] a
    grpc.WithStatsHandler option with the otelgrpc client handler. They
    satisfy the grpcutil.ServerInstrumenter and grpcutil.ClientInstrumenter
    interfaces, so an Instrumenter is passed to grpcserver.WithInstrumenter
    and grpcclient.WithInstrumenter.
  - The handlers create a span for every RPC, propagate the trace context
    through the request metadata, and record the rpc.server.* and
    rpc.client.* metrics defined by the OpenTelemetry semantic conventions.

# Providers

The global tracer, meter, and propagator providers are used by default:
[github.com/tecnickcom/nurago/pkg/metrics/opentel.New] installs them, so no
further wiring is needed. [WithTracerProvider], [WithMeterProvider], and
[WithPropagators] set explicit ones.

# Filtering

By default the RPCs of the grpc.health.v1.Health service and of the
grpc.reflection.* services are not instrumented, so health probes and
reflection clients do not flood the trace backend. Filtered RPCs produce
neither spans nor metrics. [WithFilter] adds more filters, and
[WithoutDefaultFilter] removes the default one.

# When To Use

  - A service exports OpenTelemetry traces and metrics and serves or calls
    gRPC APIs.
  - Trace context must propagate across gRPC calls as it does across HTTP
    calls.
*/
package opentelgrpc

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

// Instrumenter adds OpenTelemetry tracing and metrics to gRPC servers and
// clients.
//
// Create it with [New].
type Instrumenter struct {
	serverHandler stats.Handler
	clientHandler stats.Handler
}

// New returns an Instrumenter configured with opts. The server and client
// stats handlers are created once and shared by every server and client that
// uses the Instrumenter.
func New(opts ...Option) (*Instrumenter, error) {
	cfg := &config{}

	for _, applyOpt := range opts {
		if applyOpt == nil {
			return nil, errNilOption
		}

		err := applyOpt(cfg)
		if err != nil {
			return nil, err
		}
	}

	otelOpts := cfg.otelOptions()

	return &Instrumenter{
		serverHandler: otelgrpc.NewServerHandler(otelOpts...),
		clientHandler: otelgrpc.NewClientHandler(otelOpts...),
	}, nil
}

// ServerOptions returns the server option that adds the otelgrpc server stats
// handler.
func (i *Instrumenter) ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{grpc.StatsHandler(i.serverHandler)}
}

// DialOptions returns the dial option that adds the otelgrpc client stats
// handler.
func (i *Instrumenter) DialOptions() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithStatsHandler(i.clientHandler)}
}

// DefaultFilter returns the filter applied unless [WithoutDefaultFilter] is
// set. It excludes the RPCs of the grpc.health.v1.Health service and of the
// services whose name starts with "grpc.reflection.".
func DefaultFilter() otelgrpc.Filter {
	return filters.None(
		filters.ServiceName("grpc.health.v1.Health"),
		filters.ServicePrefix("grpc.reflection."),
	)
}
