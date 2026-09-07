/*
Package prometheus implements [github.com/tecnickcom/nurago/pkg/metrics.Client]
using the Prometheus client ecosystem.

It registers a set of default collectors and exposes wrappers for common
instrumentation points:

  - inbound HTTP handlers
  - outbound HTTP round trippers
  - SQL database stats
  - application counters (log level and error code dimensions)

The implementation is based on:
  - github.com/prometheus/client_golang/prometheus
  - github.com/prometheus/client_golang/prometheus/promhttp
  - github.com/dlmiddlecote/sqlstats

# Default Metrics Scope

Default collectors include:
  - Go runtime and process metrics
  - HTTP server request count, in-flight gauge, duration histogram, request
    size histogram, and response size histogram
  - HTTP client request count, in-flight gauge, and duration histogram
  - error counters by level and by task/operation/code

# When To Use

  - Your platform scrapes metrics rather than receiving pushes.
  - You want standard HTTP, SQL, and error metrics without defining
    collectors by hand.
*/
package prometheus
