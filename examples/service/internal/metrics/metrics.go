// Package metrics defines the instrumentation metrics for this program.
package metrics

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tecnickcom/nurago/pkg/grpcutil"
	"github.com/tecnickcom/nurago/pkg/metrics"
	prom "github.com/tecnickcom/nurago/pkg/metrics/prometheus"
	"github.com/tecnickcom/nurago/pkg/metrics/prometheus/prometheusgrpc"
)

const (
	// NameExample is the name of an example custom collector.
	NameExample = "example_collector"

	labelCode = "code"
)

// grpcMetricsNewFn defines the gRPC metrics constructor and can be overwritten for testing.
var grpcMetricsNewFn = prometheusgrpc.New //nolint:gochecknoglobals

// errMetricsClientNotInitialized is returned when a method requiring the
// underlying metrics client is called before CreateMetricsClientFunc.
var errMetricsClientNotInitialized = errors.New("metrics client not initialized: call CreateMetricsClientFunc first")

// Metrics is the interface for the custom metrics.
type Metrics interface {
	CreateMetricsClientFunc() (metrics.Client, error)
	GRPCServerInstrumenter() (grpcutil.ServerInstrumenter, error)
	IncExampleCounter(code string)
	InstrumentDB(dbName string, db *sql.DB) error
	SqlOpen(driverName, dsn string) (*sql.DB, error)
}

// Client groups the custom collectors to be shared with other packages.
type Client struct {
	libClient metrics.Client
	// grpcMetrics holds the gRPC server metrics.
	grpcMetrics *prometheusgrpc.Metrics
	// collectorExample is an example collector.
	collectorExample *prometheus.CounterVec
}

// New creates a metrics client wrapper with service-specific collectors that
// integrate with the shared nurago metrics client.
func New() *Client {
	return &Client{
		collectorExample: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: NameExample,
				Help: "Example of custom collector.",
			},
			[]string{labelCode},
		),
	}
}

// CreateMetricsClientFunc constructs the underlying metrics client and
// registers all custom collectors, including the gRPC server metrics. It is
// passed to bootstrap during startup.
func (m *Client) CreateMetricsClientFunc() (metrics.Client, error) {
	grpcMetrics, err := grpcMetricsNewFn()
	if err != nil {
		return nil, fmt.Errorf("failed to create the gRPC metrics: %w", err)
	}

	opts := []prom.Option{
		prom.WithCollector(m.collectorExample),
		prom.WithCollector(grpcMetrics),
	}

	m.grpcMetrics = grpcMetrics
	m.libClient, err = prom.New(opts...)

	return m.libClient, err //nolint:wrapcheck
}

// GRPCServerInstrumenter returns the gRPC server metrics, to be passed to
// grpcserver.WithInstrumenter. It returns an error if the metrics client is not
// initialized.
func (m *Client) GRPCServerInstrumenter() (grpcutil.ServerInstrumenter, error) {
	if m.grpcMetrics == nil {
		return nil, errMetricsClientNotInitialized
	}

	return m.grpcMetrics, nil
}

// IncExampleCounter increments the example counter for a given status code
// label.
func (m *Client) IncExampleCounter(code string) {
	m.collectorExample.With(prometheus.Labels{labelCode: code}).Inc()
}

// InstrumentDB instruments a SQL connection so query and connection pool
// telemetry is exported with the provided database name. It returns an error
// if the metrics client is not initialized.
func (m *Client) InstrumentDB(dbName string, db *sql.DB) error {
	if m.libClient == nil {
		return errMetricsClientNotInitialized
	}

	return m.libClient.InstrumentDB(dbName, db) //nolint:wrapcheck
}

// SqlOpen opens a SQL database through the metrics client so returned handles
// are compatible with the service instrumentation pipeline.
func (m *Client) SqlOpen(driverName, dsn string) (*sql.DB, error) {
	if m.libClient == nil {
		return nil, errMetricsClientNotInitialized
	}

	return m.libClient.SqlOpen(driverName, dsn) //nolint:wrapcheck
}
