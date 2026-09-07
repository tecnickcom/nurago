package metrics_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/tecnickcom/nurago/pkg/metrics"
)

// service depends on the Client contract rather than on a concrete backend,
// so the same code works with Prometheus, StatsD, OpenTelemetry, or Default.
type service struct {
	mtr metrics.Client
}

func (s *service) routes() http.Handler {
	mux := http.NewServeMux()

	// The path label must be a low-cardinality route template, never a raw
	// request URI containing identifiers.
	mux.Handle("/users/", s.mtr.InstrumentHandler("/users/{id}", s.getUser))
	mux.Handle("/metrics", s.mtr.MetricsHandlerFunc())

	return mux
}

func (s *service) getUser(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("user"))
}

func ExampleClient() {
	// Default is a no-op implementation, useful in tests and in builds that
	// ship without a metrics backend. Swapping in
	// metrics/prometheus.New, metrics/statsd.New, or metrics/opentel.New
	// changes nothing else in the service.
	svc := &service{mtr: &metrics.Default{}}

	srv := httptest.NewServer(svc.routes())
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, srv.URL+"/users/42", nil)
	if err != nil {
		fmt.Println(err)

		return
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	svc.mtr.IncErrorCounter("user", "read", "404")
	svc.mtr.IncLogLevelCounter("error")

	fmt.Println(resp.StatusCode, svc.mtr.Close())

	// Output:
	// 200 <nil>
}

func ExampleDefault() {
	// Default satisfies Client without emitting anything, so instrumented
	// code paths stay exercised in unit tests.
	var client metrics.Client = &metrics.Default{}

	handler := client.InstrumentHandler("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.TODO(), http.MethodGet, "/healthz", nil))

	fmt.Println(rec.Code)

	// Output:
	// 204
}
