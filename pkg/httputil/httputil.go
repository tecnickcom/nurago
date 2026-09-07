/*
Package httputil provides HTTP request/response primitives for Go services built
on top of net/http.

The package groups helpers by role:
  - request helpers: header decorators, path/query/header default extraction,
    request-time context utilities
  - response helpers: text/JSON/XML responses with no-cache and nosniff headers,
    full-buffer encoding with a 500 fallback on marshal failure, and structured
    response logging via [HTTPResp]
  - response-writer wrapper: status/size capture and optional tee support for
    middleware instrumentation ([ResponseWriterWrapper])
  - URL composition helper for link building ([Link])

Response logs contain code, duration, timestamp, trace ID, and payload metadata,
logged by class (2xx debug, 4xx warn, 5xx error). JSend-style status projection
and round-trip parsing are provided by [StatusSuccess], [StatusFail],
[StatusError], [Status.UnmarshalJSON], and [ErrInvalidStatus].

# When To Use

  - You are writing handlers against net/http and want consistent request
    parsing and response writing.
  - Responses should follow one envelope across every endpoint.
*/
package httputil
