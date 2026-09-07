package healthcheck_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/tecnickcom/nurago/pkg/healthcheck"
)

// database stands in for any dependency exposing a HealthCheck method, which
// is the shape the healthcheck.HealthChecker interface requires. Several
// nurago clients (redis, valkey, sqs, s3, ipify, slack) already satisfy it.
type database struct {
	err error
}

func (d *database) HealthCheck(_ context.Context) error {
	return d.err
}

func ExampleNewHandler() {
	checks := []healthcheck.HealthCheck{
		healthcheck.New("database", &database{}),
		healthcheck.New("cache", healthcheck.HealthCheckFunc(func(_ context.Context) error {
			return nil
		})),
	}

	// Each check runs in its own goroutine, bounded by the handler timeout.
	handler := healthcheck.NewHandler(checks, healthcheck.WithTimeout(time.Second))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/healthz", nil,
	))

	body, _ := io.ReadAll(rec.Body)

	fmt.Println(rec.Code)
	fmt.Println(string(body))

	// Output:
	// 200
	// {"cache":"OK","database":"OK"}
}

func ExampleNewHandler_failure() {
	checks := []healthcheck.HealthCheck{
		healthcheck.New("database", &database{err: errors.New("connection refused")}),
		healthcheck.New("cache", healthcheck.HealthCheckFunc(func(_ context.Context) error {
			return nil
		})),
	}

	handler := healthcheck.NewHandler(checks)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/healthz", nil,
	))

	body, _ := io.ReadAll(rec.Body)

	// A single failing probe makes the whole endpoint report unhealthy, and
	// the per-check message identifies which dependency is down.
	fmt.Println(rec.Code)
	fmt.Println(string(body))

	// Output:
	// 503
	// {"cache":"OK","database":"connection refused"}
}

func ExampleCheckHTTPStatus() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	defer srv.Close()

	// CheckHTTPStatus probes a downstream HTTP dependency. The status must
	// equal wantStatusCode, unless WithAcceptStatus replaces that check with
	// a predicate.
	err := healthcheck.CheckHTTPStatus(
		context.TODO(),
		srv.Client(),
		http.MethodGet,
		srv.URL,
		http.StatusNoContent,
		time.Second,
	)

	fmt.Println(err)

	// Any 2xx accepted instead of one exact code.
	err = healthcheck.CheckHTTPStatus(
		context.TODO(),
		srv.Client(),
		http.MethodGet,
		srv.URL,
		http.StatusOK,
		time.Second,
		healthcheck.WithAcceptStatus(func(code int) bool {
			return code >= http.StatusOK && code < http.StatusMultipleChoices
		}),
	)

	fmt.Println(err)

	// Output:
	// <nil>
	// <nil>
}
