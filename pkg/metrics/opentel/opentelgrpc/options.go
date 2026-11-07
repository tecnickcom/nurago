package opentelgrpc

import (
	"errors"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// errNilOption is returned by New for a nil option.
var errNilOption = errors.New("option is required")

// Option configures an [Instrumenter] during [New].
type Option func(cfg *config) error

// config contains the configuration of the instrumenter.
type config struct {
	tracerProvider       trace.TracerProvider
	meterProvider        metric.MeterProvider
	propagators          propagation.TextMapPropagator
	filters              []otelgrpc.Filter
	disableDefaultFilter bool
	otelOpts             []otelgrpc.Option
}

// otelOptions returns the otelgrpc options: the providers, the combined
// filter, and the options set with WithOtelGRPCOptions.
func (c *config) otelOptions() []otelgrpc.Option {
	var opts []otelgrpc.Option

	if c.tracerProvider != nil {
		opts = append(opts, otelgrpc.WithTracerProvider(c.tracerProvider))
	}

	if c.meterProvider != nil {
		opts = append(opts, otelgrpc.WithMeterProvider(c.meterProvider))
	}

	if c.propagators != nil {
		opts = append(opts, otelgrpc.WithPropagators(c.propagators))
	}

	fs := c.filters
	if !c.disableDefaultFilter {
		fs = append([]otelgrpc.Filter{DefaultFilter()}, fs...)
	}

	if len(fs) > 0 {
		opts = append(opts, otelgrpc.WithFilter(filters.All(fs...)))
	}

	return append(opts, c.otelOpts...)
}

// WithTracerProvider sets the tracer provider used to create spans in place
// of the global one.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(cfg *config) error {
		if tp == nil {
			return errors.New("tracerProvider is required")
		}

		cfg.tracerProvider = tp

		return nil
	}
}

// WithMeterProvider sets the meter provider used to record metrics in place
// of the global one.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(cfg *config) error {
		if mp == nil {
			return errors.New("meterProvider is required")
		}

		cfg.meterProvider = mp

		return nil
	}
}

// WithPropagators sets the propagator used to read and write the trace
// context in the request metadata in place of the global one.
func WithPropagators(p propagation.TextMapPropagator) Option {
	return func(cfg *config) error {
		if p == nil {
			return errors.New("propagators is required")
		}

		cfg.propagators = p

		return nil
	}
}

// WithFilter adds a filter. An RPC is instrumented only when every filter,
// including the default one, returns true. Filters can be built with the
// otelgrpc/filters package.
func WithFilter(f otelgrpc.Filter) Option {
	return func(cfg *config) error {
		if f == nil {
			return errors.New("filter is required")
		}

		cfg.filters = append(cfg.filters, f)

		return nil
	}
}

// WithoutDefaultFilter removes the default filter (see [DefaultFilter]), so
// health and reflection RPCs are instrumented too.
func WithoutDefaultFilter() Option {
	return func(cfg *config) error {
		cfg.disableDefaultFilter = true
		return nil
	}
}

// WithOtelGRPCOptions adds otelgrpc options, applied after the ones built by
// this package. An otelgrpc.WithFilter option replaces the filters.
func WithOtelGRPCOptions(opts ...otelgrpc.Option) Option {
	return func(cfg *config) error {
		for _, o := range opts {
			if o == nil {
				return errors.New("otelGRPCOption is required")
			}
		}

		cfg.otelOpts = append(cfg.otelOpts, opts...)

		return nil
	}
}
