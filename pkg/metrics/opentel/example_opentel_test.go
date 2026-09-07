package opentel_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/tecnickcom/nurago/pkg/metrics"
	"github.com/tecnickcom/nurago/pkg/metrics/opentel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func ExampleNew() {
	// The default providers export over OTLP. The example substitutes
	// providers that discard their output, so it needs no collector.
	// Production code omits both options and configures the exporter through
	// the standard OTEL_EXPORTER_OTLP_* environment variables.
	meterFn := func(_ context.Context, res *sdkresource.Resource) (*sdkmetric.MeterProvider, error) {
		return sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewManualReader()),
		), nil
	}

	tracerFn := func(_ context.Context, res *sdkresource.Resource) (*sdktrace.TracerProvider, error) {
		return sdktrace.NewTracerProvider(sdktrace.WithResource(res)), nil
	}

	// The returned Client satisfies metrics.Client, so application code
	// depends on the contract rather than on OpenTelemetry.
	var client metrics.Client

	client, err := opentel.New(
		context.TODO(),
		"payments",
		"v1.2.3",
		opentel.WithMeterProviderFn(meterFn),
		opentel.WithTracerProviderFn(tracerFn),
	)
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

	// OpenTelemetry is push-based, so the metrics endpoint exposes no
	// scrape payload and answers with a health-style 200.
	scrape := httptest.NewRecorder()
	client.MetricsHandlerFunc()(scrape, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/metrics", nil,
	))

	fmt.Println(rec.Code, scrape.Code)

	// Output:
	// 200 200
}

func ExampleTraceID() {
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		fmt.Println(err)

		return
	}

	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		fmt.Println(err)

		return
	}

	// ContextWithSpanContext injects an existing trace, which is what an
	// inbound request carrying W3C traceparent headers provides.
	ctx := opentel.ContextWithSpanContext(context.TODO(), traceID, spanID)

	fmt.Println(opentel.TraceID(ctx))

	// A context with no span yields an empty trace ID rather than an error.
	fmt.Printf("%q\n", opentel.TraceID(context.TODO()))

	// Output:
	// 4bf92f3577b34da6a3ce929d0e0e4736
	// ""
}
