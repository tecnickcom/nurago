package metrics

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/metrics/prometheus/prometheusgrpc"
)

func TestNew(t *testing.T) {
	t.Parallel()

	m := New()
	require.NotNil(t, m, "Metrics should not be nil")
	require.NotNil(t, m.collectorExample, "collectorExample not be nil")
}

func TestCreateMetricsClientFunc(t *testing.T) {
	t.Parallel()

	m := New()
	c, err := m.CreateMetricsClientFunc()
	require.NoError(t, err, "CreateMetricsClientFunc() unexpected error = %v", err)
	require.NotNil(t, c, "metrics.Client should not be nil")
}

// TestCreateMetricsClientFuncGRPCError mutates the package-level
// grpcMetricsNewFn, so it must not run in parallel with the other tests.
//
//nolint:paralleltest
func TestCreateMetricsClientFuncGRPCError(t *testing.T) {
	oldGRPCMetricsNewFn := grpcMetricsNewFn

	defer func() { grpcMetricsNewFn = oldGRPCMetricsNewFn }()

	grpcMetricsNewFn = func(_ ...prometheusgrpc.Option) (*prometheusgrpc.Metrics, error) {
		return nil, errors.New("TEST ERROR")
	}

	m := New()
	c, err := m.CreateMetricsClientFunc()
	require.Error(t, err)
	require.Nil(t, c)
}

func TestIncExampleCounter(t *testing.T) {
	t.Parallel()

	m := New()
	i := testutil.CollectAndCount(m.collectorExample, NameExample)
	require.Equal(t, 0, i, "failed to assert right metrics: got %v want %v", i, 0)

	m.IncExampleCounter("test")
	i = testutil.CollectAndCount(m.collectorExample, NameExample)
	require.Equal(t, 1, i, "failed to assert right metrics: got %v want %v", i, 1)
}

func TestInstrumentDB(t *testing.T) {
	t.Parallel()

	m := New()
	c, err := m.CreateMetricsClientFunc()
	require.NoError(t, err, "CreateMetricsClientFunc() unexpected error = %v", err)
	require.NotNil(t, c, "metrics.Client should not be nil")

	db, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)

	err = m.InstrumentDB("db_test", db)
	require.NoError(t, err)
}

func TestInstrumentDBNotInitialized(t *testing.T) {
	t.Parallel()

	m := New()

	err := m.InstrumentDB("db_test", nil)
	require.ErrorIs(t, err, errMetricsClientNotInitialized, "expected error when the metrics client is not initialized")
}

func TestSqlOpenNotInitialized(t *testing.T) {
	t.Parallel()

	m := New()

	db, err := m.SqlOpen("sqlmock", "test_dsn")
	require.ErrorIs(t, err, errMetricsClientNotInitialized, "expected error when the metrics client is not initialized")
	require.Nil(t, db)
}

func TestSqlOpen(t *testing.T) {
	t.Parallel()

	m := New()
	c, err := m.CreateMetricsClientFunc()
	require.NoError(t, err, "CreateMetricsClientFunc() unexpected error = %v", err)
	require.NotNil(t, c, "metrics.Client should not be nil")

	dsn := "sqlmock_db_nuragoexample"

	_, _, err = sqlmock.NewWithDSN(dsn)
	require.NoError(t, err)

	db, err := m.SqlOpen("sqlmock", dsn)
	require.NoError(t, err)
	require.NotNil(t, db)

	db.Close()
}

func TestGRPCServerInstrumenter(t *testing.T) {
	t.Parallel()

	m := New()

	i, err := m.GRPCServerInstrumenter()
	require.ErrorIs(t, err, errMetricsClientNotInitialized, "expected error when the metrics client is not initialized")
	require.Nil(t, i)

	_, err = m.CreateMetricsClientFunc()
	require.NoError(t, err)

	i, err = m.GRPCServerInstrumenter()
	require.NoError(t, err)
	require.NotNil(t, i)
	require.NotEmpty(t, i.ServerOptions())
}
