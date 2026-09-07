/*
Package opentel implements [github.com/tecnickcom/nurago/pkg/metrics.Client]
using OpenTelemetry for both metrics and tracing.

It provides an OpenTelemetry-backed client with interface-compatible
instrumentation hooks:

  - inbound HTTP handler instrumentation
  - outbound HTTP transport instrumentation
  - SQL open/instrumentation helpers with otelsql
  - log-level and error-taxonomy counters

At startup, [New] configures and registers global OpenTelemetry providers
(tracer, meter, propagator), creates default counters, and records shutdown
functions so callers can terminate exporters via [Client.Close] or
[Client.CloseCtx].

# Defaults and Configuration

By default, exporter selection is environment-driven:
  - Traces: OTLP/HTTP when OTEL_EXPORTER_OTLP_TRACES_ENDPOINT or
    OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise stdout exporter.
  - Metrics: OTLP/HTTP when OTEL_EXPORTER_OTLP_METRICS_ENDPOINT or
    OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise stdout exporter.

Resource attributes are resolved from explicit parameters and environment,
including OTEL_SERVICE_NAME, OTEL_SERVICE_VERSION,
OTEL_DEPLOYMENT_ENVIRONMENT_NAME, and OTEL_RESOURCE_ATTRIBUTES.

# When To Use

  - Your platform collects OTLP and you want traces and metrics from one
    client.
  - Trace context must propagate across service boundaries via W3C headers.
*/
package opentel
