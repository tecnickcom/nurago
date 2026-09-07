/*
Package httpreverseproxy provides a reverse-proxy client built on top of
net/http/httputil.ReverseProxy.

The package wraps ReverseProxy behind a [Client]:
  - [New] configures proxy behavior from an upstream base address.
  - [Client.ForwardRequest] forwards incoming HTTP requests to the target.

When no custom rewrite function is provided, requests are rewritten to the
configured upstream URL and the wildcard `path` segment is forwarded as the
proxied path. Standard `X-Forwarded-*` headers are set automatically. A custom
reverse proxy and HTTP transport can be supplied via [WithReverseProxy] and
[WithHTTPClient]; the default error handler logs upstream failures and returns
HTTP 502 Bad Gateway.

# Defaults and Behavior

The default upstream client never follows redirects (3xx responses are forwarded
verbatim, avoiding an SSRF vector) and uses a private, tuned transport that raises
the per-host idle-connection pool and bounds only the wait for response headers
(via ResponseHeaderTimeout). Because there is no whole-request timeout, streaming
responses (Server-Sent Events, long downloads) and slow uploads are forwarded
without being truncated; a disconnecting client still cancels the upstream request.

The default rewrite sets the outbound Host header to the upstream host and appends
to any inbound `X-Forwarded-*` headers, so those should be trusted only behind a
trusted hop. Only the scheme, host, and base path of the configured upstream address
are used; any userinfo or query string in it is dropped. Percent-encoded reserved
characters in the path (notably `%2F`) are decoded before forwarding, because routing
operates on the decoded path.

When the address carries a base path, that base path acts as a boundary by default:
a request whose path resolves outside it (via `.` / `..`) is rejected with HTTP 400
before the upstream is contacted. Pass [WithLaxBasePath] to restore transparent
forwarding of `.` / `..` segments (a pass-through proxy where the upstream is the
authorization boundary). The check is a no-op with a custom rewrite/director or when
the address carries no base path.

The check resolves `.` / `..` (via path.Clean) only to make the accept/reject
decision; a request that is accepted is still forwarded verbatim (its trailing slash
and any in-bounds `.` / `..` segments are preserved for the upstream to normalize).
The boundary therefore assumes the upstream resolves paths the same way, and it does
not defend against multiply percent-encoded traversal (e.g. `%252e%252e`), which
survives a single decode as a literal segment, so put untrusted-input defenses at
the upstream as well.

# Observability Behavior

The default error handler logs request method/path/query, trace ID, response
code/status, request/response timing, and the underlying proxy error. The logged
path and query are those of the outbound (rewritten) upstream request, since
ReverseProxy passes the outbound request to the handler. The query and the URL
embedded in the error are redacted via the configured [WithRedactFn] (default
redact.Default().BytesToString) so query-parameter secrets do not leak into logs. If request
start time is present in context (via httputil request-time helpers), response
duration is computed from it.

A genuine upstream failure is logged at Error level and answered with HTTP 502.
When the client went away before the upstream responded (the inbound request context
is canceled or its deadline elapsed), it is not an upstream fault: it is logged at
Info level under a distinct message with the non-standard 499 code and no response is
written to the abandoned connection.

Only transport failures and base-path rejections are logged; a successfully forwarded
request (including one where the upstream returns a 4xx or 5xx response) produces no
entry here, because that is a successful round trip. Add access logging by wrapping
[Client.ForwardRequest] in middleware, or set ModifyResponse on a proxy passed via
[WithReverseProxy].

# When To Use

  - A service fronts an internal upstream and needs to forward requests with
    consistent logging.
  - You want httputil.ReverseProxy behavior without repeating the director
    and error-handler boilerplate.
*/
package httpreverseproxy
