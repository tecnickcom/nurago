package opentelgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestOtelOptions_default(t *testing.T) {
	t.Parallel()

	cfg := &config{}

	require.Len(t, cfg.otelOptions(), 1, "the default filter only")

	require.NoError(t, WithoutDefaultFilter()(cfg))
	require.True(t, cfg.disableDefaultFilter)
	require.Empty(t, cfg.otelOptions())
}

func TestWithProviders(t *testing.T) {
	t.Parallel()

	cfg := &config{}

	require.Error(t, WithTracerProvider(nil)(cfg))
	require.Error(t, WithMeterProvider(nil)(cfg))
	require.Error(t, WithPropagators(nil)(cfg))

	require.NoError(t, WithTracerProvider(sdktrace.NewTracerProvider())(cfg))
	require.NoError(t, WithMeterProvider(sdkmetric.NewMeterProvider())(cfg))
	require.NoError(t, WithPropagators(propagation.TraceContext{})(cfg))

	require.NotNil(t, cfg.tracerProvider)
	require.NotNil(t, cfg.meterProvider)
	require.NotNil(t, cfg.propagators)
	require.Len(t, cfg.otelOptions(), 4)
}

func TestWithFilter(t *testing.T) {
	t.Parallel()

	cfg := &config{}

	require.Error(t, WithFilter(nil)(cfg))
	require.Empty(t, cfg.filters)

	require.NoError(t, WithFilter(filters.MethodName("A"))(cfg))
	require.NoError(t, WithFilter(filters.MethodName("B"))(cfg))
	require.Len(t, cfg.filters, 2)
	require.Len(t, cfg.otelOptions(), 1, "the filters are combined into one option")
}

func TestWithOtelGRPCOptions(t *testing.T) {
	t.Parallel()

	cfg := &config{}

	require.Error(t, WithOtelGRPCOptions(otelgrpc.WithMessageEvents(otelgrpc.SentEvents), nil)(cfg))
	require.Empty(t, cfg.otelOpts)

	require.NoError(t, WithOtelGRPCOptions(otelgrpc.WithMessageEvents(otelgrpc.SentEvents))(cfg))
	require.Len(t, cfg.otelOpts, 1)
	require.Len(t, cfg.otelOptions(), 2)
}
