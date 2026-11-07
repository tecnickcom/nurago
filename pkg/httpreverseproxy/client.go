package httpreverseproxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	libhttputil "github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/logutil"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/traceid"
)

// HTTPClient is the transport used by [Client] for outgoing proxied requests.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultPathParam is the default name of the catch-all route parameter
// holding the upstream path. It can be overridden via WithPathParam.
const defaultPathParam = "path"

// defaultResponseHeaderTimeout bounds how long the default upstream transport
// waits for response headers from a stuck upstream. It is applied at the transport
// level rather than as an http.Client.Timeout so it does not cap the response-body
// transfer: streaming responses (Server-Sent Events, long downloads) and slow
// uploads are forwarded without being truncated mid-flight.
const defaultResponseHeaderTimeout = 1 * time.Minute

// defaultMaxIdleConnsPerHost raises the per-host idle-connection pool of the
// default upstream transport above net/http's default of 2. A reverse proxy by
// design concentrates traffic on a small set of upstream hosts, so the default
// would throttle connection reuse under load. It mirrors the sibling httpclient
// package's tuning.
const defaultMaxIdleConnsPerHost = 100

// statusClientClosedRequest is the non-standard status code (popularized by nginx)
// used only in logs to mark a request whose client disconnected before the upstream
// responded. It is never written back to the client.
const statusClientClosedRequest = 499

// statusClientClosedText is the log message paired with statusClientClosedRequest;
// http.StatusText does not define text for non-standard codes.
const statusClientClosedText = "Client Closed Request"

// ErrInvalidAddress is returned by [New] when the upstream base address used by the
// default rewrite lacks a http/https scheme or a host. It wraps the offending
// address for context.
var ErrInvalidAddress = errors.New("httpreverseproxy: invalid upstream address")

// Client implements the Reverse Proxy.
type Client struct {
	proxy         *httputil.ReverseProxy
	httpClient    HTTPClient
	logger        *slog.Logger
	redactFn      RedactFn
	pathParam     string
	basePath      string
	flushInterval time.Duration
	laxBasePath   bool
}

// New constructs reverse proxy client forwarding requests to upstream address with default rewrite and X-Forwarded-* headers.
//
// The default rewrite builds the upstream path from a catch-all route parameter
// named "path" (e.g. a route registered as "/proxy/*path"). If the router
// registers the catch-all under a different name, configure it via WithPathParam,
// otherwise the upstream path silently becomes "/".
//
// The default rewrite is installed only when the supplied reverse proxy has neither
// a Rewrite nor a Director configured, so a custom [WithReverseProxy] using either
// mechanism is left intact (ReverseProxy rejects having both set). The upstream
// address is validated only when the default rewrite is used; an address without a
// http/https scheme or a host yields [ErrInvalidAddress].
//
// The default rewrite sets the outbound Host header to the upstream host and, via
// SetXForwarded, appends to any inbound X-Forwarded-* headers, so trust those only
// behind a trusted hop. Any userinfo or query string in the configured upstream
// address is dropped; only its scheme, host, and base path are used. Percent-encoded
// reserved characters in the path (notably %2F) are decoded before forwarding,
// because routing operates on the decoded path. When the address carries a base
// path, requests whose path resolves outside it (via "." / "..") are rejected with
// HTTP 400 by default; pass [WithLaxBasePath] to forward them verbatim instead.
func New(addr string, opts ...Option) (*Client, error) {
	c := &Client{
		pathParam: defaultPathParam,
		redactFn:  redact.Default().BytesToString,
	}

	for _, applyOpt := range opts {
		applyOpt(c)
	}

	if c.proxy == nil {
		c.proxy = &httputil.ReverseProxy{}
	}

	err := c.ensureRewrite(addr)
	if err != nil {
		return nil, err
	}

	c.ensureTransport()
	c.ensureLogging()

	if c.flushInterval != 0 {
		c.proxy.FlushInterval = c.flushInterval
	}

	return c, nil
}

// ForwardRequest forwards HTTP request to configured upstream service via reverse proxy.
//
// When the upstream address carries a base path, a request whose path would resolve
// outside that base path (e.g. via "..") is rejected with HTTP 400 before the
// upstream is contacted. Pass [WithLaxBasePath] to disable this check and forward
// such paths verbatim.
func (c *Client) ForwardRequest(w http.ResponseWriter, r *http.Request) {
	if !c.laxBasePath && c.basePath != "" && !c.pathWithinBase(r) {
		c.rejectTraversal(w, r)

		return
	}

	c.proxy.ServeHTTP(w, r)
}

// CloseIdleConnections closes any idle upstream connections held by the underlying
// transport. It is safe for concurrent use and no-ops for transports that do not
// implement the optional CloseIdleConnections method.
func (c *Client) CloseIdleConnections() {
	if ic, ok := c.proxy.Transport.(interface{ CloseIdleConnections() }); ok {
		ic.CloseIdleConnections()
	}
}

// ensureRewrite installs the default rewrite when the proxy has no request-rewriting
// mechanism configured, validating the upstream address and recording its base path.
func (c *Client) ensureRewrite(addr string) error {
	// Director is deprecated in favor of Rewrite, but it must still be checked:
	// ReverseProxy requires exactly one of the two, so installing the default
	// Rewrite over a caller-supplied Director would fail every request.
	if c.proxy.Rewrite != nil || c.proxy.Director != nil { //nolint:staticcheck // SA1019: intentional deprecated-field guard
		return nil
	}

	proxyURL, err := parseUpstreamURL(addr)
	if err != nil {
		return err
	}

	c.basePath = proxyURL.Path
	c.proxy.Rewrite = c.defaultRewrite(proxyURL)

	return nil
}

// ensureTransport installs the default upstream transport when the proxy has none,
// wrapping a caller-supplied HTTPClient when provided.
func (c *Client) ensureTransport() {
	if c.proxy.Transport == nil {
		if c.httpClient == nil {
			c.httpClient = defaultUpstreamClient()
		}

		c.proxy.Transport = &httpWrapper{client: c.httpClient}
	}
}

// ensureLogging installs the default logger, error log, and error handler for any
// of those the caller left unset (a custom reverse proxy keeps its own).
func (c *Client) ensureLogging() {
	if c.logger == nil {
		c.logger = slog.Default()
	}

	if c.proxy.ErrorLog == nil {
		c.proxy.ErrorLog = logutil.NewLogFromSlog(c.logger)
	}

	if c.proxy.ErrorHandler == nil {
		c.proxy.ErrorHandler = c.newErrorHandler()
	}
}

// defaultRewrite returns the default ProxyRequest rewrite that targets proxyURL,
// preserving its base path while appending the catch-all route segment and setting
// standard X-Forwarded-* headers.
func (c *Client) defaultRewrite(proxyURL *url.URL) func(*httputil.ProxyRequest) {
	return func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme = proxyURL.Scheme
		r.Out.URL.Host = proxyURL.Host
		// Preserve the base path of the configured upstream address (e.g. "/v2")
		// while replacing the inbound route prefix with the catch-all path
		// parameter. RawPath is cleared so EscapedPath re-derives the encoding from
		// the freshly built Path instead of a stale inbound value.
		r.Out.URL.Path = proxyURL.Path + "/" + libhttputil.PathParam(r.Out, c.pathParam)
		r.Out.URL.RawPath = ""
		r.Out.Host = proxyURL.Host
		r.SetXForwarded()
	}
}

// pathWithinBase reports whether the outbound path built for r stays within the
// configured base path once "." and ".." segments are resolved.
func (c *Client) pathWithinBase(r *http.Request) bool {
	cleaned := path.Clean(c.basePath + "/" + libhttputil.PathParam(r, c.pathParam))

	return cleaned == c.basePath || strings.HasPrefix(cleaned, c.basePath+"/")
}

// rejectTraversal logs a base-path escape attempt and answers HTTP 400.
//
// The request is rejected before any rewrite, so request_path here is the inbound
// path the client sent (e.g. "/proxy/../admin"), unlike the "proxy_error" entry,
// whose request_path is the rewritten upstream path.
func (c *Client) rejectTraversal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	c.logger.LogAttrs(ctx, slog.LevelWarn, "proxy_path_rejected",
		slog.String(traceid.DefaultLogKey, traceid.FromContext(ctx, "")),
		slog.String("request_method", r.Method),
		slog.String("request_path", r.URL.Path),
		slog.String("request_query", c.redactFn([]byte(r.URL.RawQuery))),
		slog.Int("response_code", http.StatusBadRequest),
	)

	http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
}

// newErrorHandler returns the default reverse-proxy error handler.
//
// A request whose inbound context is already done (canceled or its deadline
// exceeded) means the client went away before the upstream responded; that is not
// an upstream fault, so it is logged at Info level and no 502 is written to the
// abandoned connection. This is detected from the inbound context state rather than
// the error, because an upstream ResponseHeaderTimeout also reports as a deadline
// error while the client is still connected, and that case must remain a 502.
//
// Genuine upstream failures are logged at Error level and answered with HTTP 502.
// The request query and the URL embedded in a *url.Error are redacted before logging
// (see WithRedactFn). Redaction does not cover the request path (a secret in a path
// segment such as "/reset/{token}" is logged as-is) nor an upstream error that is not
// a *url.Error (a custom HTTPClient must redact its own error messages).
func (c *Client) newErrorHandler() errHandler {
	logger := c.logger

	redactFn := c.redactFn
	if redactFn == nil {
		redactFn = redact.Default().BytesToString
	}

	return func(w http.ResponseWriter, r *http.Request, err error) {
		now := time.Now()
		resTime := now.UTC()
		ctx := r.Context()

		reqTime, ok := libhttputil.GetRequestTimeFromContext(ctx)
		if !ok {
			reqTime = now
		}

		clientGone := ctx.Err() != nil

		code := http.StatusBadGateway
		message := http.StatusText(http.StatusBadGateway)
		logMsg := "proxy_error"
		level := slog.LevelError

		if clientGone {
			code = statusClientClosedRequest
			message = statusClientClosedText
			logMsg = "proxy_client_closed"
			level = slog.LevelInfo
		}

		// request_path/request_query reflect the outbound (rewritten) upstream
		// request, since ReverseProxy passes the outbound request to the handler.
		logger.LogAttrs(ctx, level, logMsg,
			slog.String(traceid.DefaultLogKey, traceid.FromContext(ctx, "")),
			slog.String("request_method", r.Method),
			slog.String("request_path", r.URL.Path),
			slog.String("request_query", redactFn([]byte(r.URL.RawQuery))),
			slog.Int("response_code", code),
			slog.String("response_message", message),
			slog.Any("response_status", libhttputil.Status(code)),
			slog.Time("request_time", reqTime.UTC()),
			slog.Time("response_time", resTime),
			slog.Duration("response_duration", now.Sub(reqTime)),
			slog.Any("error", redactErrorForLog(err, redactFn)),
		)

		// Nothing useful can be written to a client that already disconnected, so
		// the 502 is emitted only for genuine upstream failures.
		if !clientGone {
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		}
	}
}

// parseUpstreamURL parses and validates the upstream base address used by the
// default rewrite. A trailing slash is trimmed so the base path joins cleanly with
// the forwarded catch-all segment.
func parseUpstreamURL(addr string) (*url.URL, error) {
	addr = strings.TrimRight(addr, "/")

	proxyURL, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid service address %q: %w", addr, err)
	}

	// url.Parse accepts inputs like "" or "localhost:8080" (which parses the host
	// as the scheme, leaving no host) without error, so the resulting proxy would
	// silently fail only at request time. Reject them up front instead.
	if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		return nil, fmt.Errorf("%w %q: scheme must be http or https", ErrInvalidAddress, addr)
	}

	if proxyURL.Host == "" {
		return nil, fmt.Errorf("%w %q: missing host", ErrInvalidAddress, addr)
	}

	// Normalize the base path so the strict base-path check (which compares against
	// a cleaned outbound path) and the forwarded base path stay consistent: a raw
	// base like "/a/../b" would otherwise never match the cleaned outbound "/b/...",
	// rejecting every request. path.Clean("") is ".", so only clean a non-empty path.
	if proxyURL.Path != "" {
		proxyURL.Path = path.Clean(proxyURL.Path)
	}

	return proxyURL, nil
}

// redactErrorForLog returns a log-safe copy of err. A failed request yields a
// *url.Error whose message embeds the full outbound URL; its query string is
// redacted so query-parameter secrets do not leak into error logs. The error
// returned to the caller (ReverseProxy) is left untouched; only the logged copy is
// redacted. Any error that does not wrap a *url.Error is returned as-is.
func redactErrorForLog(err error, redactFn RedactFn) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}

	cp := *uerr
	cp.URL = redactQueryTail(uerr.URL, redactFn)

	return &cp
}

// redactQueryTail redacts the query portion (everything after the first '?') of a
// raw URL string, leaving the path untouched.
func redactQueryTail(raw string, redactFn RedactFn) string {
	q := strings.IndexByte(raw, '?')
	if q < 0 {
		return raw
	}

	return raw[:q+1] + redactFn([]byte(raw[q+1:]))
}

// defaultUpstreamClient returns the default HTTP client used to forward requests
// upstream.
//
// A reverse-proxy client must never follow redirects: 3xx responses are forwarded
// verbatim to the client instead of being fetched by the proxy itself (an SSRF
// vector). Timeouts are bounded at the transport level (see defaultUpstreamTransport)
// rather than via http.Client.Timeout, which would also cap the response-body
// transfer and truncate streaming responses.
func defaultUpstreamClient() *http.Client {
	return &http.Client{
		Transport: defaultUpstreamTransport(),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// defaultUpstreamTransport returns a private transport for the default upstream
// client. It clones http.DefaultTransport so per-proxy tuning never mutates the
// process-wide transport (which would race across concurrent New calls and change
// behavior for every other consumer), raises the per-host idle-connection pool, and
// bounds the wait for upstream response headers.
func defaultUpstreamTransport() http.RoundTripper {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// http.DefaultTransport is always an *http.Transport in the standard
		// library; this guards against a test override or a future change.
		return http.DefaultTransport
	}

	clone := t.Clone()
	clone.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	clone.ResponseHeaderTimeout = defaultResponseHeaderTimeout

	return clone
}

// httpWrapper adapts [HTTPClient] to the [http.RoundTripper] interface.
type httpWrapper struct {
	client HTTPClient
}

// RoundTrip implements the RoundTripper interface.
func (c *httpWrapper) RoundTrip(r *http.Request) (*http.Response, error) {
	// Request.RequestURI can't be set in client requests.
	// Ref.: https://github.com/golang/go/blob/f3c39a83a3076eb560c7f687cbb35eef9b506e7d/src/net/http/client.go#L219
	r.RequestURI = ""

	return c.client.Do(r) //nolint:wrapcheck
}

// CloseIdleConnections forwards to the wrapped client's CloseIdleConnections when it
// implements one, so [Client.CloseIdleConnections] reaches the default *http.Client.
func (c *httpWrapper) CloseIdleConnections() {
	if ic, ok := c.client.(interface{ CloseIdleConnections() }); ok {
		ic.CloseIdleConnections()
	}
}

// errHandler defines the function signature for error handlers.
type errHandler = func(w http.ResponseWriter, r *http.Request, err error)
