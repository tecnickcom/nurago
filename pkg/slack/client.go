package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tecnickcom/nurago/pkg/httpretrier"
	"github.com/tecnickcom/nurago/pkg/httputil"
)

// Default configuration values.
const (
	// defaultPingURL is the canonical Slack status endpoint. status.slack.com
	// 301-redirects here, so using it directly avoids the extra hop and works
	// even with a custom client that does not follow redirects.
	defaultPingURL     = "https://slack-status.com/api/v2.0.0/current"
	defaultTimeout     = 1 * time.Second
	defaultPingTimeout = 1 * time.Second

	// statusOK is the top-level status the Slack status API returns when there
	// is no ongoing incident; any other value means at least one active incident.
	statusOK = "ok"

	// incidentStatusResolved marks an already-closed incident within the
	// active_incidents array.
	incidentStatusResolved = "resolved"

	// failService is the Slack service whose active incidents make this client's
	// webhook delivery unreliable; HealthCheck flags only incidents affecting it.
	failService = "Apps/Integrations/APIs"

	// defaultMaxRetryAfter caps how long a server-provided Retry-After header can
	// stall a retry, so a rate-limited or misconfigured endpoint cannot force a
	// very long wait on a caller that did not set a context deadline.
	defaultMaxRetryAfter = 60 * time.Second

	// maxBodyBytes caps how much of a response body is decoded or drained (for
	// keep-alive connection reuse), bounding memory if a misconfigured or hostile
	// endpoint returns a very large body. The status API response can legitimately
	// be several KiB, so the cap is generous.
	maxBodyBytes = 1 << 20 // 1 MiB

	// maxErrBodyBytes caps the response-body snippet included in send errors,
	// keeping error messages (and logs) small.
	maxErrBodyBytes = 512
)

// HTTPClient is the minimal HTTP transport contract used by [Client].
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client sends Slack webhook messages and performs Slack status health checks.
type Client struct {
	httpClient    HTTPClient
	retrier       *httpretrier.HTTPRetrier
	address       string
	timeout       time.Duration
	pingTimeout   time.Duration
	retryDelay    time.Duration
	retryAttempts uint
	pingURL       string
	username      string
	iconEmoji     string
	iconURL       string
	channel       string
}

// New constructs a Slack webhook client with defaults for timeout, retries, and optional message metadata.
// Parameters other than addr are optional defaults that can be overridden per Send call.
func New(addr, username, iconEmoji, iconURL, channel string, opts ...Option) (*Client, error) {
	address, err := parseWebhookAddr(addr)
	if err != nil {
		return nil, err
	}

	c := &Client{
		address:       address.String(),
		timeout:       defaultTimeout,
		pingTimeout:   defaultPingTimeout,
		retryDelay:    httpretrier.DefaultDelay,
		retryAttempts: httpretrier.DefaultAttempts,
		pingURL:       defaultPingURL,
		username:      username,
		iconEmoji:     iconEmoji,
		iconURL:       iconURL,
		channel:       channel,
	}

	for _, applyOpt := range opts {
		applyOpt(c)
	}

	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: c.timeout}
	}

	// The retrier holds only immutable configuration, so a single instance is
	// shared by all Send calls; building it here surfaces invalid retry settings
	// at construction time instead of on every send. Retry-After is honored (and
	// capped) so a 429 from Slack backs off for at least the server-requested time.
	c.retrier, err = httpretrier.New(
		c.httpClient,
		httpretrier.WithRetryIfFn(httpretrier.RetryIfForWriteRequests),
		httpretrier.WithAttempts(c.retryAttempts),
		httpretrier.WithDelay(c.retryDelay),
		httpretrier.WithRespectRetryAfter(),
		httpretrier.WithMaxRetryAfter(defaultMaxRetryAfter),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRetryConfig, err)
	}

	return c, nil
}

// parseWebhookAddr parses and validates the webhook address, returning errors
// that match [ErrInvalidAddress] without leaking the address. The webhook URL
// is a secret, so the raw addr is never echoed: url.ParseRequestURI returns a
// *url.Error whose Error() embeds addr, so only its underlying parse reason is
// surfaced.
func parseWebhookAddr(addr string) (*url.URL, error) {
	address, err := url.ParseRequestURI(addr)
	if err != nil {
		reason := err

		if uerr, ok := errors.AsType[*url.Error](err); ok {
			reason = uerr.Err
		}

		return nil, fmt.Errorf("%w: %w", ErrInvalidAddress, reason)
	}

	// Reject relative or schemeless addresses at construction time; otherwise
	// every Send would fail at request time with an obscure transport error.
	if address.Scheme == "" || address.Host == "" {
		return nil, fmt.Errorf("%w: missing scheme or host", ErrInvalidAddress)
	}

	return address, nil
}

// status models the subset of the Slack status API v2.0.0 "current" response
// that HealthCheck inspects. See https://slack-status.com/api/v2.0.0/current
// (status.slack.com 301-redirects there).
type status struct {
	// Status is "ok" when there is no ongoing incident, or "active" otherwise.
	Status string `json:"status"`

	// ActiveIncidents lists the currently ongoing incidents.
	ActiveIncidents []incident `json:"active_incidents"`
}

// incident models one entry of the status API "active_incidents" array.
type incident struct {
	Status   string   `json:"status"`   // "active" or "resolved"
	Title    string   `json:"title"`    // human-readable summary
	URL      string   `json:"url"`      // link to the incident page
	Services []string `json:"services"` // affected Slack service names
}

// HealthCheck verifies the Slack status endpoint is reachable and reports no
// active incident affecting webhook delivery.
//
// It confirms the status endpoint returns HTTP 200 with a decodable body, then
// inspects active_incidents and returns an error only when an ongoing (not
// "resolved") incident affects the Apps/Integrations/APIs service, since that is
// the service webhook delivery depends on. Unrelated Slack incidents do not fail
// the check. The response shape follows the Slack status API v2.0.0 "current"
// endpoint.
func (c *Client) HealthCheck(ctx context.Context) (err error) {
	ctx, cancel := context.WithTimeout(ctx, c.pingTimeout)
	defer cancel()

	req, nerr := http.NewRequestWithContext(ctx, http.MethodGet, c.pingURL, nil)
	if nerr != nil {
		return fmt.Errorf("build request: %w", nerr)
	}

	resp, derr := c.httpClient.Do(req)
	if derr != nil {
		return fmt.Errorf("healthcheck request: %w", derr)
	}

	defer func() {
		// Drain any remaining body (capped) so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		err = errors.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected healthcheck status code: %d", resp.StatusCode)
	}

	respBody := &status{}

	// Cap the decoded input so a hostile or misconfigured endpoint cannot force a
	// very large allocation via a single giant JSON value.
	rerr := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(respBody)
	if rerr != nil {
		return fmt.Errorf("failed decoding response body: %w", rerr)
	}

	if respBody.Status == statusOK {
		return nil
	}

	if inc := apiServiceIncident(respBody.ActiveIncidents); inc != nil {
		return fmt.Errorf("slack incident affecting %q: %s (%s)", failService, inc.Title, inc.URL)
	}

	return nil
}

// apiServiceIncident returns the first ongoing incident affecting failService,
// or nil if none. Entries in active_incidents are ongoing, so only those
// explicitly marked "resolved" are skipped (tolerant of a missing per-incident
// status field), and only incidents affecting failService are reported.
func apiServiceIncident(incidents []incident) *incident {
	for i := range incidents {
		// Entries in active_incidents are ongoing, so skip only those explicitly
		// marked resolved, then report the first one affecting failService.
		if incidents[i].Status == incidentStatusResolved {
			continue
		}

		if slices.Contains(incidents[i].Services, failService) {
			return &incidents[i]
		}
	}

	return nil
}

// message is the outgoing webhook payload schema.
type message struct {
	Text      string `json:"text"`
	Username  string `json:"username,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	Channel   string `json:"channel,omitempty"`
}

// Send posts a message to Slack webhook, using client defaults for empty metadata arguments.
func (c *Client) Send(ctx context.Context, text, username, iconEmoji, iconURL, channel string) error {
	reqData := &message{
		Text:      text,
		Username:  httputil.StringValueOrDefault(username, c.username),
		IconEmoji: httputil.StringValueOrDefault(iconEmoji, c.iconEmoji),
		IconURL:   httputil.StringValueOrDefault(iconURL, c.iconURL),
		Channel:   httputil.StringValueOrDefault(channel, c.channel),
	}

	return c.sendData(ctx, reqData)
}

// sendData serializes and posts the webhook payload with retry and status validation.
func (c *Client) sendData(ctx context.Context, reqData *message) (err error) {
	reqBody, _ := json.Marshal(reqData) //nolint:errchkjson

	r, nerr := http.NewRequestWithContext(ctx, http.MethodPost, c.address, bytes.NewReader(reqBody))
	if nerr != nil {
		// URL parse errors quote the full webhook address (a secret): redact it.
		return c.redactWebhookURL(fmt.Errorf("create request: %w", nerr))
	}

	r.Header.Set(httputil.HeaderContentType, httputil.MimeTypeJSON)

	resp, derr := c.retrier.Do(r)
	if derr != nil {
		// Transport failures are wrapped by Go's HTTP client in *url.Error,
		// whose Error() includes the full request URL. The Slack webhook URL
		// path is a secret, so redact it before returning.
		return c.redactWebhookURL(fmt.Errorf("execute request: %w", derr))
	}

	defer func() {
		err = errors.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode != http.StatusOK {
		// Include a capped snippet of the response body: Slack reports the reason
		// for a rejected webhook there (e.g. "invalid_payload"), which aids
		// debugging. The body does not contain the secret webhook URL.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		if body = bytes.TrimSpace(body); len(body) > 0 {
			return fmt.Errorf("slack client error - Code: %v, Status: %v, Body: %s", resp.StatusCode, resp.Status, body)
		}

		return fmt.Errorf("slack client error - Code: %v, Status: %v", resp.StatusCode, resp.Status)
	}

	// Drain (capped) so the connection can be reused by keep-alive.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))

	return nil
}

// redactWebhookURL returns an error whose message has every occurrence of the
// client's webhook address replaced with "REDACTED". This guards against the
// secret leaking through wrapped transport errors: Go's HTTP client returns a
// *url.Error whose Error() string contains the full request URL, and the path
// of a Slack webhook URL is a secret. The original error is preserved via
// Unwrap so callers can still inspect it with errors.Is / errors.As.
func (c *Client) redactWebhookURL(err error) error {
	if err == nil {
		return nil
	}

	if !strings.Contains(err.Error(), c.address) {
		return err
	}

	return &redactedError{
		msg: strings.ReplaceAll(err.Error(), c.address, "REDACTED"),
		err: err,
	}
}

// redactedError wraps an error with a sanitized message while keeping the
// original error reachable through Unwrap for errors.Is / errors.As checks.
type redactedError struct {
	err error
	msg string
}

// Error returns the sanitized error message with the webhook URL redacted.
func (e *redactedError) Error() string { return e.msg }

// Unwrap exposes the original error for errors.Is / errors.As.
func (e *redactedError) Unwrap() error { return e.err }
