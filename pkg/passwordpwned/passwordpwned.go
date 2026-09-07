/*
Package passwordpwned checks whether a password has appeared in a known data
breach, using the Have I Been Pwned (HIBP) Pwned Passwords API v3
(https://haveibeenpwned.com/API/v3#PwnedPasswords).

It uses the HIBP k-anonymity model: only the first 5 hex characters of the
SHA-1 hash are sent over the network. The API returns all hash suffixes that
share that 5-character prefix (typically 800 to 1,000 entries due to
Add-Padding). The full hash match is resolved on the client side, so the
password and its complete hash never leave the process.

Create a client with [New] and call [Client.IsPwnedPassword]:

	c, err := passwordpwned.New()
	if err != nil {
		log.Fatal(err)
	}

	pwned, err := c.IsPwnedPassword(ctx, password)
	if err != nil {
		log.Fatal(err)
	}

	if pwned {
		return errors.New("password has been compromised in a data breach, please choose another")
	}

To enforce a threshold policy instead (e.g. NIST-style "reject only if seen
more than N times"), use [Client.PwnedCount]:

	count, err := c.PwnedCount(ctx, password)
	if err != nil {
		log.Fatal(err)
	}

	if count > maxAllowedBreaches {
		return errors.New("password is too common, please choose another")
	}

# Features

Requests set the Add-Padding header, so every response contains 800 to 1,000
entries regardless of the real match count. Responses are requested as brotli
and decoded per their declared Content-Encoding (brotli, gzip, or identity);
the decoded body is capped (see [WithResponseSizeLimit]) to guard against
decompression-bomb style memory exhaustion. A 200 response that is not
structurally valid range data (e.g. a captive-portal HTML page) is rejected
with [ErrMalformedResponse] instead of being read as "not pwned". A
[httpretrier]-backed retry policy handles transient network errors for
read-only requests and honors a server-provided Retry-After header.
[Client.PwnedCount] returns the raw breach count for threshold policies;
[Client.IsPwnedPassword] returns a boolean. [Client.HealthCheck] performs a
bounded range request to verify the endpoint is reachable. [WithURL],
[WithTimeout], [WithHTTPClient], [WithUserAgent], [WithRetryAttempts],
[WithRetryDelay], [WithResponseSizeLimit], and [WithPingTimeout] configure the
client. [HTTPClient] is an interface, so a mock HTTP client can be injected in
tests.

# Security Note

The SHA-1 algorithm is used solely because the HIBP API requires it. The
password is hashed locally; only the 5-character prefix is sent over TLS.
This package does not store, log, or persist the password or its hash.

# When To Use

  - Registration and password-change flows should reject known-breached
    passwords.
  - A breach-count threshold suits your policy better than a flat reject.
*/
package passwordpwned

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	brotli "github.com/aperturerobotics/go-brotli-decoder"
)

const (
	// prefixLen is the number of leading hex characters of the SHA-1 hash sent
	// to the API (the k-anonymity prefix).
	prefixLen = 5

	// suffixLen is the number of trailing hex characters of the SHA-1 hash
	// (40 total minus the 5-char prefix) matched client-side against a response line.
	suffixLen = 35
)

// Sentinel errors returned by this package. Match them with [errors.Is].
var (
	// ErrInvalidURL is returned by [New] when the configured API base URL cannot
	// be parsed, lacks a scheme or host, or contains a query or fragment.
	ErrInvalidURL = errors.New("invalid API base URL")

	// ErrInvalidUserAgent is returned by [New] when the User-Agent is empty or
	// contains control characters: Go suppresses an empty User-Agent header
	// entirely (and the HIBP API rejects requests without one), while control
	// bytes are rejected by the HTTP transport on every request.
	ErrInvalidUserAgent = errors.New("invalid user agent")

	// ErrMalformedResponse is returned when the response body is empty, missing,
	// not structurally valid HIBP range data, or a matched hash suffix is not
	// followed by the expected ":<count>" sequence.
	ErrMalformedResponse = errors.New("malformed response body")

	// ErrUnexpectedStatus is returned when the API responds with a non-200
	// status code; the code is included in the wrapped message.
	ErrUnexpectedStatus = errors.New("unexpected status code")

	// ErrResponseTooLarge is returned when the decoded response exceeds the
	// configured limit (see [WithResponseSizeLimit]), guarding against
	// decompression-bomb style memory exhaustion.
	ErrResponseTooLarge = errors.New("response body exceeds size limit")

	// ErrUnsupportedEncoding is returned when the server responds with a
	// Content-Encoding this client cannot decode.
	ErrUnsupportedEncoding = errors.New("unsupported response content-encoding")
)

// IsPwnedPassword reports whether password has appeared in a known data breach.
//
// It is a thin wrapper over [Client.PwnedCount]: it returns true when the breach
// count is greater than zero.
//
// Returns (true, nil) if the password is found in the breach database.
// Returns (false, nil) if the password is not found or its breach count is zero.
// Returns (false, err) on any network, encoding, or unexpected HTTP error.
func (c *Client) IsPwnedPassword(ctx context.Context, password string) (bool, error) {
	count, err := c.PwnedCount(ctx, password)

	return count > 0, err
}

// PwnedCount returns how many times password appears in the HIBP Pwned Passwords
// breach database, or 0 if it is not present (including padding entries, which
// always report a count of 0). This lets callers enforce threshold policies
// (e.g. NIST-style "reject if seen more than N times") rather than a bare boolean.
//
// The lookup uses the HIBP k-anonymity model:
//  1. The SHA-1 hash of password is computed locally.
//  2. Only the first 5 hex characters of the hash are sent to the HIBP API.
//  3. The API returns all matching hash suffixes (padded to 800 to 1,000 entries).
//  4. The full hash is matched client-side; the password never leaves the process.
//
// Returns a non-nil error on any network, encoding, size-limit, or unexpected
// HTTP error; the count is 0 in that case.
func (c *Client) PwnedCount(ctx context.Context, password string) (int, error) {
	// The hash is computed locally per call (no shared hash.Hash state), so
	// concurrent calls on the same Client are safe.
	sum := sha1.Sum([]byte(password)) //nolint:gosec // SHA-1 is required by the HIBP API.

	hash := strings.ToUpper(hex.EncodeToString(sum[:]))

	data, err := c.fetchRange(ctx, hash[:prefixLen])
	if err != nil {
		return 0, err
	}

	return matchCount(data, hash[prefixLen:])
}

// fetchRange requests the range endpoint for the given hash prefix and returns
// the decoded (size-bounded, uppercase-normalized) response body.
func (c *Client) fetchRange(ctx context.Context, prefix string) ([]byte, error) {
	r, nerr := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL+"/"+rangePath+"/"+prefix, nil)
	if nerr != nil {
		return nil, fmt.Errorf("create request: %w", nerr)
	}

	r.Header.Set("User-Agent", c.userAgent)
	r.Header.Set("Accept-Encoding", "br") // Prefer brotli-encoded responses.
	r.Header.Set("Add-Padding", "true")   // All responses will contain between 800 and 1,000 results regardless of the number of hash suffixes returned by the service.

	resp, derr := c.retrier.Do(r)
	if derr != nil {
		return nil, fmt.Errorf("execute request: %w", derr)
	}

	// A nil body from a non-conforming injected HTTPClient must not panic in the
	// drain/close or read paths below (net/http itself never returns one).
	if resp.Body == nil {
		return nil, ErrMalformedResponse
	}

	// Drain (bounded, so a hostile server cannot stall the caller) and close so
	// the connection can be reused; the close error is intentionally ignored, as
	// the meaningful result is already in hand and must not mask it.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxResponseBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	reader, rderr := decodeBody(resp)
	if rderr != nil {
		return nil, rderr
	}

	return readRangeBody(reader, c.maxResponseBytes)
}

// readRangeBody reads the decoded stream enforcing the size cap, uppercases
// ASCII hex letters in place so responses from lowercase-hex mirrors still
// match (HIBP itself returns uppercase suffixes), and validates that the body
// is structurally valid range data.
func readRangeBody(reader io.Reader, limit int64) ([]byte, error) {
	// Read one byte past the limit so an over-limit body is detectable.
	data, rerr := io.ReadAll(io.LimitReader(reader, limit+1))
	if rerr != nil {
		return nil, fmt.Errorf("error decoding response body: %w", rerr)
	}

	if int64(len(data)) > limit {
		return nil, ErrResponseTooLarge
	}

	for i, b := range data {
		if b >= 'a' && b <= 'f' {
			data[i] = b - ('a' - 'A')
		}
	}

	// A well-formed HIBP range response always contains padded entries, so an
	// empty or non-range body (e.g. an HTML page served with status 200 by a
	// captive portal or a misrouted mirror) signals a broken response rather
	// than "not pwned": for a breach check, "can't verify" must never read as
	// "verified safe".
	if !validRangeStart(data) {
		return nil, ErrMalformedResponse
	}

	return data, nil
}

// validRangeStart reports whether data begins with "<35-hex>:<digit>", the
// shape of every HIBP range line, rejecting well-formed non-range bodies that
// would otherwise silently resolve to "not pwned". Checking only the first line
// is deliberate: it is O(1) regardless of body size, and any response opening
// with a valid range line came from something speaking the range protocol.
// It expects data to be already uppercase-normalized.
func validRangeStart(data []byte) bool {
	if len(data) < suffixLen+2 {
		return false
	}

	for _, b := range data[:suffixLen] {
		if (b < '0' || b > '9') && (b < 'A' || b > 'F') {
			return false
		}
	}

	return data[suffixLen] == ':' && data[suffixLen+1] >= '0' && data[suffixLen+1] <= '9'
}

// decodeBody selects a reader for the response body based on its declared
// Content-Encoding. Setting Accept-Encoding manually disables Go's transparent
// gzip handling, so every encoding a server (or a re-encoding mirror/proxy) may
// legitimately return (brotli, gzip, or identity) must be handled explicitly;
// any other encoding is rejected.
func decodeBody(resp *http.Response) (io.Reader, error) {
	switch enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc {
	case "br":
		return brotli.NewReader(resp.Body), nil
	case "gzip":
		gz, gerr := gzip.NewReader(resp.Body)
		if gerr != nil {
			return nil, fmt.Errorf("error creating gzip reader: %w", gerr)
		}

		return gz, nil
	case "", "identity":
		return resp.Body, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEncoding, enc)
	}
}

// matchCount locates suffix in the range response body and returns its breach
// count. It returns 0 when the suffix is absent (not pwned) and
// [ErrMalformedResponse] when a matched suffix is not followed by a valid
// ":<count>" sequence.
func matchCount(data []byte, suffix string) (int, error) {
	idx := bytes.Index(data, []byte(suffix))

	// A password is not pwned if the hash suffix is not found.
	if idx < 0 {
		return 0, nil
	}

	// Each response line is "<35-hex-char suffix>:<count>": the matched suffix
	// must be followed by a ':' separator and at least one count digit.
	// Guard against truncated or malformed responses before indexing.
	sep := idx + suffixLen
	if (sep+1 >= len(data)) || (data[sep] != ':') {
		return 0, ErrMalformedResponse
	}

	return parseCount(data, sep+1)
}

// parseCount reads the decimal breach count beginning at start, stopping at the
// first non-digit (the line terminator). A missing or non-numeric count yields
// [ErrMalformedResponse].
func parseCount(data []byte, start int) (int, error) {
	end := start
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}

	count, err := strconv.Atoi(string(data[start:end]))
	if err != nil {
		return 0, ErrMalformedResponse
	}

	return count, nil
}
