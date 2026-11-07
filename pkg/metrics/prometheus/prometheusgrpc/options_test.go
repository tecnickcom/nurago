package prometheusgrpc

import (
	"context"
	"math"
	"testing"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	want := []float64{0.001, 0.01, 0.1, 1, 10, 100}
	require.InDeltaSlice(t, want, cfg.serverBuckets, 1e-9)
	require.InDeltaSlice(t, want, cfg.clientBuckets, 1e-9)
	require.False(t, cfg.disableHistogram)
	require.Nil(t, cfg.exemplarFn)
	require.Len(t, cfg.serverMetricsOptions(), 1)
	require.Len(t, cfg.clientMetricsOptions(), 1)
	require.Len(t, cfg.interceptorOptions(), 1)
	require.Len(t, cfg.rpcFilters(), 1)
	require.Nil(t, noContextLabels(context.Background()))
}

func TestValidateBuckets(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateBuckets([]float64{0.1}))
	require.NoError(t, validateBuckets([]float64{-1, 0, 1, math.Inf(1)}))
	require.Error(t, validateBuckets(nil))
	require.Error(t, validateBuckets([]float64{1, 1}))
	require.Error(t, validateBuckets([]float64{2, 1}))
	require.Error(t, validateBuckets([]float64{math.NaN()}))
}

func TestWithHandlingTimeBuckets(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithServerHandlingTimeBuckets(nil)(cfg))
	require.Error(t, WithClientHandlingTimeBuckets([]float64{2, 1})(cfg))

	buckets := []float64{0.5, 1}

	require.NoError(t, WithServerHandlingTimeBuckets(buckets)(cfg))
	require.NoError(t, WithClientHandlingTimeBuckets(buckets)(cfg))

	buckets[0] = 0

	require.InDeltaSlice(t, []float64{0.5, 1}, cfg.serverBuckets, 1e-9, "the slice is copied")
	require.InDeltaSlice(t, []float64{0.5, 1}, cfg.clientBuckets, 1e-9, "the slice is copied")
}

func TestWithoutHandlingTimeHistogram(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.NoError(t, WithoutHandlingTimeHistogram()(cfg))
	require.True(t, cfg.disableHistogram)
	require.Empty(t, cfg.serverMetricsOptions())
	require.Empty(t, cfg.clientMetricsOptions())
}

func TestWithExemplarFromContext(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithExemplarFromContext(nil)(cfg))
	require.NoError(t, WithExemplarFromContext(func(context.Context) prometheus.Labels { return nil })(cfg))
	require.NotNil(t, cfg.exemplarFn)
	require.Len(t, cfg.interceptorOptions(), 2)
}

func TestWithMetricsOptions(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithServerMetricsOptions(grpcprom.WithContextLabels("a"), nil)(cfg))
	require.Error(t, WithClientMetricsOptions(nil)(cfg))
	require.Error(t, WithInterceptorOptions(nil)(cfg))
	require.Empty(t, cfg.serverOpts)
	require.Empty(t, cfg.clientOpts)
	require.Empty(t, cfg.interceptorOpts)

	require.NoError(t, WithServerMetricsOptions(grpcprom.WithContextLabels("a"))(cfg))
	require.NoError(t, WithClientMetricsOptions(grpcprom.WithClientContextLabels("a"))(cfg))
	require.NoError(t, WithInterceptorOptions(grpcprom.WithLabelsFromContext(func(context.Context) prometheus.Labels {
		return nil
	}))(cfg))

	require.Len(t, cfg.serverMetricsOptions(), 2)
	require.Len(t, cfg.clientMetricsOptions(), 2)
	require.Len(t, cfg.interceptorOptions(), 2)
}

func TestWithFilter(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	require.Error(t, WithFilter(nil)(cfg))
	require.NoError(t, WithFilter(func(string) bool { return true })(cfg))
	require.Len(t, cfg.rpcFilters(), 2)

	require.NoError(t, WithoutDefaultFilter()(cfg))
	require.Len(t, cfg.rpcFilters(), 1)
}
