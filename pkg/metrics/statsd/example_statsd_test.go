package statsd_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/tecnickcom/nurago/pkg/metrics"
	"github.com/tecnickcom/nurago/pkg/metrics/statsd"
)

func ExampleNew() {
	// A local UDP socket stands in for the StatsD daemon so the example
	// needs no external process.
	var lc net.ListenConfig

	conn, err := lc.ListenPacket(context.TODO(), "udp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = conn.Close() }()

	// The returned Client satisfies metrics.Client, so application code
	// depends on the contract rather than on StatsD.
	var client metrics.Client

	client, err = statsd.New(
		statsd.WithNetwork("udp"),
		statsd.WithAddress(conn.LocalAddr().String()),
		statsd.WithPrefix("payments."),
		statsd.WithFlushPeriod(100*time.Millisecond),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = client.Close() }()

	client.IncErrorCounter("user", "read", "404")
	client.IncLogLevelCounter("error")

	// StatsD is push-based and exposes no scrape payload, so the metrics
	// endpoint returns 501 Not Implemented.
	rec := httptest.NewRecorder()
	client.MetricsHandlerFunc()(rec, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/metrics", nil,
	))

	fmt.Println(rec.Code)

	// Output:
	// 501
}
