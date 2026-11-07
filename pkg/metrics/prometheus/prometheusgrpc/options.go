package prometheusgrpc

import (
	"context"
	"errors"
	"math"
	"slices"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
)

// errNilOption is returned by New for a nil option.
var errNilOption = errors.New("option is required")

// Filter reports whether the RPC of fullMethod ("/package.Service/Method") is
// instrumented.
type Filter = func(fullMethod string) bool

// ExemplarFunc returns the exemplar labels attached to the metrics of an RPC,
// or nil for none. It is called once per RPC with the RPC context.
type ExemplarFunc = func(ctx context.Context) prometheus.Labels

// Option configures a [Metrics] during [New].
type Option func(cfg *config) error

// config contains the configuration of the gRPC metrics.
type config struct {
	serverBuckets    []float64
	clientBuckets    []float64
	disableHistogram bool
	exemplarFn       ExemplarFunc
	filters          []Filter
	noDefaultFilter  bool
	serverOpts       []grpcprom.ServerMetricsOption
	clientOpts       []grpcprom.ClientMetricsOption
	interceptorOpts  []grpcprom.Option
}

// defaultConfig returns the default configuration. The histogram buckets
// match the HTTP duration buckets of the metrics/prometheus package.
func defaultConfig() *config {
	return &config{
		serverBuckets: prometheus.ExponentialBuckets(0.001, 10, 6),
		clientBuckets: prometheus.ExponentialBuckets(0.001, 10, 6),
	}
}

// serverMetricsOptions returns the upstream server metrics options: the
// handling time histogram, followed by the options set with
// WithServerMetricsOptions.
func (c *config) serverMetricsOptions() []grpcprom.ServerMetricsOption {
	var opts []grpcprom.ServerMetricsOption

	if !c.disableHistogram {
		opts = append(opts, grpcprom.WithServerHandlingTimeHistogram(
			grpcprom.WithHistogramBuckets(c.serverBuckets),
		))
	}

	return append(opts, c.serverOpts...)
}

// clientMetricsOptions returns the upstream client metrics options: the
// handling time histogram, followed by the options set with
// WithClientMetricsOptions.
func (c *config) clientMetricsOptions() []grpcprom.ClientMetricsOption {
	var opts []grpcprom.ClientMetricsOption

	if !c.disableHistogram {
		opts = append(opts, grpcprom.WithClientHandlingTimeHistogram(
			grpcprom.WithHistogramBuckets(c.clientBuckets),
		))
	}

	return append(opts, c.clientOpts...)
}

// rpcFilters returns the default filter, unless WithoutDefaultFilter is set,
// followed by the filters set with WithFilter.
func (c *config) rpcFilters() []Filter {
	if c.noDefaultFilter {
		return c.filters
	}

	return append([]Filter{DefaultFilter()}, c.filters...)
}

// interceptorOptions returns the upstream interceptor options: a labels
// function that returns no labels, the exemplar function, and the options set
// with WithInterceptorOptions. The labels function sets every context label
// to "" unless WithInterceptorOptions sets another one.
func (c *config) interceptorOptions() []grpcprom.Option {
	opts := []grpcprom.Option{grpcprom.WithLabelsFromContext(noContextLabels)}

	if c.exemplarFn != nil {
		opts = append(opts, grpcprom.WithExemplarFromContext(c.exemplarFn))
	}

	return append(opts, c.interceptorOpts...)
}

// noContextLabels returns no context labels.
func noContextLabels(context.Context) prometheus.Labels {
	return nil
}

// WithServerHandlingTimeBuckets sets the upper bounds, in seconds, of the
// grpc_server_handling_seconds histogram buckets. The bounds must be in
// strictly increasing order. The slice is copied.
func WithServerHandlingTimeBuckets(buckets []float64) Option {
	return func(cfg *config) error {
		err := validateBuckets(buckets)
		if err != nil {
			return err
		}

		cfg.serverBuckets = slices.Clone(buckets)

		return nil
	}
}

// WithClientHandlingTimeBuckets sets the upper bounds, in seconds, of the
// grpc_client_handling_seconds histogram buckets. The bounds must be in
// strictly increasing order. The slice is copied.
func WithClientHandlingTimeBuckets(buckets []float64) Option {
	return func(cfg *config) error {
		err := validateBuckets(buckets)
		if err != nil {
			return err
		}

		cfg.clientBuckets = slices.Clone(buckets)

		return nil
	}
}

// validateBuckets rejects an empty list of bucket upper bounds, or one that is
// not in strictly increasing order.
func validateBuckets(buckets []float64) error {
	if len(buckets) == 0 {
		return errors.New("histogram buckets are required")
	}

	prev := math.Inf(-1)

	for _, b := range buckets {
		if math.IsNaN(b) || b <= prev {
			return errors.New("histogram buckets must be in strictly increasing order")
		}

		prev = b
	}

	return nil
}

// WithoutHandlingTimeHistogram disables the grpc_server_handling_seconds and
// grpc_client_handling_seconds histograms. The counters are not affected.
func WithoutHandlingTimeHistogram() Option {
	return func(cfg *config) error {
		cfg.disableHistogram = true
		return nil
	}
}

// WithExemplarFromContext sets the function that returns the exemplar labels
// attached to the counters and histograms of an RPC, for example
// [TraceIDExemplar]. The labels must be valid exemplar labels: the prometheus
// client panics when their names and values exceed 128 UTF-8 characters in
// total.
func WithExemplarFromContext(fn ExemplarFunc) Option {
	return func(cfg *config) error {
		if fn == nil {
			return errors.New("exemplarFn is required")
		}

		cfg.exemplarFn = fn

		return nil
	}
}

// WithServerMetricsOptions adds upstream server metrics options, applied after
// the ones built by this package. A grpcprom.WithServerHandlingTimeHistogram
// option replaces the default histogram configuration.
func WithServerMetricsOptions(opts ...grpcprom.ServerMetricsOption) Option {
	return func(cfg *config) error {
		for _, o := range opts {
			if o == nil {
				return errors.New("serverMetricsOption is required")
			}
		}

		cfg.serverOpts = append(cfg.serverOpts, opts...)

		return nil
	}
}

// WithClientMetricsOptions adds upstream client metrics options, applied after
// the ones built by this package. A grpcprom.WithClientHandlingTimeHistogram
// option replaces the default histogram configuration.
func WithClientMetricsOptions(opts ...grpcprom.ClientMetricsOption) Option {
	return func(cfg *config) error {
		for _, o := range opts {
			if o == nil {
				return errors.New("clientMetricsOption is required")
			}
		}

		cfg.clientOpts = append(cfg.clientOpts, opts...)

		return nil
	}
}

// WithInterceptorOptions adds upstream interceptor options, applied after the
// exemplar function set with WithExemplarFromContext. Use it to set
// grpcprom.WithLabelsFromContext, which sets the values of the context labels
// added with WithServerMetricsOptions or WithClientMetricsOptions. Without it,
// the context labels are "".
func WithInterceptorOptions(opts ...grpcprom.Option) Option {
	return func(cfg *config) error {
		for _, o := range opts {
			if o == nil {
				return errors.New("interceptorOption is required")
			}
		}

		cfg.interceptorOpts = append(cfg.interceptorOpts, opts...)

		return nil
	}
}

// WithFilter adds a filter. An RPC is instrumented only when every filter,
// including the default one, returns true. The filters apply to server and
// client RPCs, and to the series created by InitializeServer.
func WithFilter(f Filter) Option {
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
		cfg.noDefaultFilter = true
		return nil
	}
}
