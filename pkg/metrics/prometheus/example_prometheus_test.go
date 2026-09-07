package prometheus_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/tecnickcom/nurago/pkg/metrics"
	"github.com/tecnickcom/nurago/pkg/metrics/prometheus"
)

func ExampleNew() {
	// The returned Client satisfies metrics.Client, so application code
	// depends on the contract rather than on Prometheus.
	var client metrics.Client

	client, err := prometheus.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = client.Close() }()

	// The path label must be a low-cardinality route template, never a raw
	// request URI containing identifiers.
	handler := client.InstrumentHandler("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/users/42", nil,
	))

	client.IncErrorCounter("user", "read", "404")

	// The metrics endpoint returns the scrape payload, since Prometheus is
	// a pull-based backend.
	scrape := httptest.NewRecorder()
	client.MetricsHandlerFunc()(scrape, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/metrics", nil,
	))

	body, _ := io.ReadAll(scrape.Body)

	fmt.Println(rec.Code, scrape.Code)
	fmt.Println(strings.Contains(string(body), prometheus.NameAPIRequests))

	// Output:
	// 200 200
	// true
}

func ExampleWithInboundRequestDurationBuckets() {
	// Histogram buckets are tuned to the latency profile of the service, so
	// the default spread does not have to fit every workload.
	client, err := prometheus.New(
		prometheus.WithInboundRequestDurationBuckets([]float64{0.005, 0.01, 0.05, 0.1, 0.5, 1}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = client.Close() }()

	fmt.Println(client != nil)

	// Output:
	// true
}
