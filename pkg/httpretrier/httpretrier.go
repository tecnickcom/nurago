/*
Package httpretrier provides configurable retry execution for outbound HTTP
requests.

It wraps an [HTTPClient] with retry orchestration driven by a pluggable
[RetryIfFn]. [HTTPRetrier.Do] executes a request up to a bounded number of
attempts, applying delay growth and jitter between retries. The retry decision
function receives both `*http.Response` and `error`, enabling policy decisions
based on transport failures and/or HTTP status codes.

Retries are applied per request rather than per client, so an already
configured client (instrumented, authenticated, or both) keeps its behavior and
is wrapped instead of replaced.

# Built-in Retry Policies

Predefined helpers are provided for common semantics:
  - [RetryIfForReadRequests] for idempotent reads (e.g. GET)
  - [RetryIfForWriteRequests] for state-changing writes (e.g. POST/PUT/PATCH)
  - [RetryIfFnByHTTPMethod] to select one of the above from method name

The default policy retries only when `err != nil`.

# Backoff Behavior

Delay progression is configurable via:
  - total attempts cap ([WithAttempts])
  - initial delay ([WithDelay])
  - multiplicative delay factor ([WithDelayFactor])
  - random jitter ceiling ([WithJitter])
  - maximum delay ceiling ([WithMaxDelay])
  - jitter strategy ([WithJitterStrategy])

This produces bounded exponential-style backoff with randomization, helping
reduce synchronized retry storms. Optionally, [WithRespectRetryAfter] makes the
retrier wait at least the server-provided Retry-After delay, and [WithOnRetry]
exposes each scheduled retry for logging or metrics.

# Request Body Replay

When a request has a body and retries are needed, the retrier relies on
`Request.GetBody` to recreate the body stream for subsequent attempts. If the
request has a body that cannot be recreated (GetBody missing or failing),
retries cannot continue and Do returns an error. Bodyless requests (e.g. a GET
with no body) retry without restriction.

# When To Use

  - Calls to an upstream API fail intermittently with 429 or 5xx responses.
  - Read and write requests need different retry policies, because replaying
    a write may duplicate a side effect.
  - The server sends Retry-After and you want it respected, bounded by a cap
    you choose.
*/
package httpretrier

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tecnickcom/nurago/pkg/backoff"
)

const (
	// DefaultAttempts is the default maximum number of total attempts (the
	// initial request plus retries).
	DefaultAttempts = 4

	// DefaultDelay is the delay to apply after the first failed attempt.
	DefaultDelay = 1 * time.Second

	// DefaultDelayFactor is the default multiplication factor to get the successive delay value.
	DefaultDelayFactor = 2

	// DefaultJitter is the maximum random Jitter time between retries.
	DefaultJitter = 100 * time.Millisecond

	// DefaultMaxDelay is the default upper bound for the computed backoff delay
	// (before jitter). It prevents the exponential growth from overflowing.
	DefaultMaxDelay = 30 * time.Second

	// DefaultMaxRetryAfter is the default cap applied to a server-provided
	// Retry-After delay (see [WithRespectRetryAfter] and [WithMaxRetryAfter]).
	DefaultMaxRetryAfter = 24 * time.Hour
)

// ErrBodyNotReplayable is returned by [HTTPRetrier.Do] when a retry is
// required but the request body has already been consumed and Request.GetBody
// is not available to recreate it.
var ErrBodyNotReplayable = errors.New("cannot retry: the request body has already been consumed and Request.GetBody is not set")

// RetryIfFn decides whether a request should be retried after a response/error.
// It must not panic; it runs inline in [HTTPRetrier.Do], before the response body
// is closed, so a panic would also leak that response.
type RetryIfFn func(r *http.Response, err error) bool

// OnRetryFn is an optional observability callback invoked before each scheduled
// retry. It receives the number of the attempt that just failed (1-based), the
// delay before the next attempt, and the response/error that triggered the retry
// (the response body is already closed). It counts scheduled retries (one may
// still be preempted by cancellation before it runs) and must not panic; it runs
// inline in [HTTPRetrier.Do].
type OnRetryFn func(attempt uint, delay time.Duration, r *http.Response, err error)

// JitterStrategy selects how backoff jitter is applied. See [backoff.JitterStrategy].
type JitterStrategy = backoff.JitterStrategy

// Jitter strategies, re-exported from [backoff] so callers need not import it.
const (
	JitterAdditive = backoff.JitterAdditive // fixed additive ceiling (default)
	JitterFull     = backoff.JitterFull     // rand[0, delay): best decorrelation
	JitterEqual    = backoff.JitterEqual    // delay/2 + rand[0, delay/2)
)

// HTTPClient is the minimal client contract used by [HTTPRetrier].
type HTTPClient interface {
	// Do performs the HTTP request. As with [net/http.Client.Do], a non-nil error
	// implies a nil response and vice versa.
	Do(req *http.Request) (*http.Response, error)
}

// HTTPRetrier applies configurable retry policies to HTTP requests.
//
// An HTTPRetrier holds only immutable configuration once constructed, so a
// single instance can be safely shared across goroutines and used for
// concurrent or overlapping [HTTPRetrier.Do] calls. All per-call mutable state
// is kept in a separate [doState] value created inside Do.
type HTTPRetrier struct {
	delayFactor       float64
	delay             time.Duration
	jitter            time.Duration
	maxDelay          time.Duration
	attempts          uint
	strategy          JitterStrategy
	respectRetryAfter bool
	maxRetryAfter     time.Duration
	retryIfFn         RetryIfFn
	onRetry           OnRetryFn
	httpClient        HTTPClient
}

// doState holds all the mutable state for a single [HTTPRetrier.Do] call so
// that a shared HTTPRetrier remains race-free across concurrent calls.
type doState struct {
	sched             *backoff.Schedule
	timer             *time.Timer
	doResponse        *http.Response
	doError           error
	remainingAttempts uint
}

// defaultHTTPRetrier creates a retrier instance with default values.
func defaultHTTPRetrier() *HTTPRetrier {
	return &HTTPRetrier{
		attempts:      DefaultAttempts,
		delay:         DefaultDelay,
		delayFactor:   DefaultDelayFactor,
		jitter:        DefaultJitter,
		maxDelay:      DefaultMaxDelay,
		maxRetryAfter: DefaultMaxRetryAfter,
		strategy:      JitterAdditive, // preserve the historical additive-jitter behavior
		retryIfFn:     defaultRetryIf,
	}
}

// New constructs HTTP retrier wrapping client with exponential backoff retry orchestration from provided options.
func New(httpClient HTTPClient, opts ...Option) (*HTTPRetrier, error) {
	c := defaultHTTPRetrier()

	for _, applyOpt := range opts {
		err := applyOpt(c)
		if err != nil {
			return nil, err
		}
	}

	c.httpClient = httpClient

	return c, nil
}

// Do executes request up to configured attempts with exponential delay and jitter between retries, applying retry decision function to each response.
//
// Do is safe for concurrent use of a single HTTPRetrier instance: every call
// keeps its own mutable state in a local [doState]. Each concurrent call must use
// its own *http.Request, since a retry mutates the request's body.
//
// An already-canceled request context fails fast. If the context is canceled just
// as a retry timer fires, one further attempt may run (with the canceled request)
// before Do returns the context error.
func (c *HTTPRetrier) Do(r *http.Request) (*http.Response, error) {
	ctxErr := r.Context().Err()
	if ctxErr != nil {
		return nil, fmt.Errorf("request context ended before first attempt: %w", ctxErr)
	}

	s := &doState{
		timer: time.NewTimer(1 * time.Nanosecond),
		sched: backoff.New(backoff.Config{
			Base:     c.delay,
			Factor:   c.delayFactor,
			Jitter:   c.jitter,
			MaxDelay: c.maxDelay,
			Strategy: c.strategy,
		}),
		remainingAttempts: c.attempts,
	}
	defer s.timer.Stop()

	for {
		select {
		case <-r.Context().Done():
			return nil, fmt.Errorf("request context ended: %w", r.Context().Err())
		case <-s.timer.C:
			if c.run(r, s) {
				return s.doResponse, s.doError
			}
		}
	}
}

// defaultRetryIf is the default retry policy: returns true only when error is not nil (transport failures).
func defaultRetryIf(_ *http.Response, err error) bool {
	return err != nil
}

// RetryIfForWriteRequests is a retry policy for state-changing requests: retries on 429/502/503 or transport error.
func RetryIfForWriteRequests(r *http.Response, err error) bool {
	if err != nil {
		return true
	}

	if r == nil {
		return false
	}

	switch r.StatusCode {
	case
		http.StatusTooManyRequests,    // 429
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable: // 503
		return true
	}

	return false
}

// RetryIfForReadRequests is a retry policy for idempotent requests: retries on 404/408/409/423/425/429/500/502/503/504/507 or transport error.
// It deliberately retries some codes that are often terminal (404/409/423): for a
// read this targets read-after-write eventual consistency, where the resource may
// materialize on a retry. Use a custom [RetryIfFn] if that is not desired.
func RetryIfForReadRequests(r *http.Response, err error) bool {
	if err != nil {
		return true
	}

	if r == nil {
		return false
	}

	switch r.StatusCode {
	case
		http.StatusNotFound,            // 404
		http.StatusRequestTimeout,      // 408
		http.StatusConflict,            // 409
		http.StatusLocked,              // 423
		http.StatusTooEarly,            // 425
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		http.StatusInsufficientStorage: // 507
		return true
	}

	return false
}

// RetryIfFnByHTTPMethod selects retry policy by HTTP method: idempotent read policy for GET, write policy for all others.
func RetryIfFnByHTTPMethod(httpMethod string) RetryIfFn {
	if httpMethod == http.MethodGet {
		return RetryIfForReadRequests
	}

	return RetryIfForWriteRequests
}

// run performs a single attempt to execute the HTTP request.
func (c *HTTPRetrier) run(r *http.Request, s *doState) bool {
	if !c.reopenBodyForRetry(r, s) {
		return true
	}

	s.doResponse, s.doError = c.httpClient.Do(r) //nolint:bodyclose

	s.remainingAttempts--
	if s.remainingAttempts == 0 || !c.retryIfFn(s.doResponse, s.doError) {
		if s.doError != nil {
			// Uphold Do's response-XOR-error contract even for a non-conforming
			// client that returned a response alongside an error.
			s.dropResponse()
		}

		return true
	}

	if !closeAndCheckReplay(r, s) {
		return true
	}

	c.scheduleRetry(r, s)

	return false
}

// reopenBodyForRetry recreates the request body immediately before a retry
// attempt (lazily, so a retry that is later aborted, e.g. by cancellation,
// never leaves an opened body dangling). It is a no-op on the first attempt or
// for a request with no GetBody, and returns false (setting doError) if GetBody
// fails.
func (c *HTTPRetrier) reopenBodyForRetry(r *http.Request, s *doState) bool {
	if s.remainingAttempts == c.attempts || r.GetBody == nil {
		return true
	}

	bodyRC, err := r.GetBody()
	if err != nil {
		s.doResponse = nil
		s.doError = fmt.Errorf("error while reading request body: %w", err)

		return false
	}

	r.Body = bodyRC

	return true
}

// closeAndCheckReplay closes the current attempt's response body (so its
// connection is never leaked) and verifies the request body can be replayed. It
// returns false (setting doError) to stop retrying on a close failure or a
// consumed body that cannot be recreated.
func closeAndCheckReplay(r *http.Request, s *doState) bool {
	if s.doResponse != nil && s.doResponse.Body != nil {
		cerr := s.doResponse.Body.Close()
		if cerr != nil {
			s.doResponse = nil
			s.doError = fmt.Errorf("error while closing response body: %w", cerr)

			return false
		}
	}

	if r.GetBody == nil && r.Body != nil {
		// The body has already been consumed and cannot be recreated:
		// retries cannot continue, as documented.
		s.doResponse = nil
		s.doError = ErrBodyNotReplayable

		return false
	}

	return true
}

// dropResponse closes and clears the response so an errored result never carries a
// non-nil response (upholding Do's response-XOR-error contract even for a
// non-conforming client).
func (s *doState) dropResponse() {
	if s.doResponse != nil && s.doResponse.Body != nil {
		_ = s.doResponse.Body.Close()
	}

	s.doResponse = nil
}

// scheduleRetry computes the next delay (honoring Retry-After when enabled),
// invokes the onRetry hook if set, and re-arms the timer for the next attempt.
func (c *HTTPRetrier) scheduleRetry(r *http.Request, s *doState) {
	if r.Context().Err() != nil {
		// The context ended during the attempt: Do's loop will observe it and
		// return the context error. Skip scheduling and the onRetry report.
		return
	}

	// Advance the exponential schedule on every retry; if Retry-After overrides the
	// value below, the consumed step only ever makes later schedule delays larger.
	delay := s.sched.Next()

	if c.respectRetryAfter {
		ra, ok := retryAfterDelay(s.doResponse, time.Now(), c.maxRetryAfter)
		if ok && ra > delay {
			// Honor the server's Retry-After, adding jitter on top so clients that all
			// received the same value do not re-synchronize. The jitter is always
			// additive (the WithJitter ceiling) regardless of WithJitterStrategy, since
			// only additive jitter preserves the "wait at least Retry-After" guarantee.
			delay = backoff.AddJitter(ra, c.jitter)
		}
	}

	if c.onRetry != nil {
		c.onRetry(c.attempts-s.remainingAttempts, delay, s.doResponse, s.doError)
	}

	// The timer just fired and was drained by Do's receive; on Go 1.23+ Reset
	// re-arms it with no stale-value risk, so no Stop/drain dance is needed.
	s.timer.Reset(delay)
}

// retryAfterDelay extracts the delay requested by a response's Retry-After header
// (delta-seconds or an HTTP-date relative to now). It returns false when the
// header is absent, malformed, or non-positive; the result is capped at maxRA.
func retryAfterDelay(resp *http.Response, now time.Time, maxRA time.Duration) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}

	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}

	secs, serr := strconv.ParseInt(v, 10, 64)
	if serr == nil {
		if secs <= 0 {
			return 0, false
		}

		if secs > int64(maxRA/time.Second) {
			return maxRA, true
		}

		return time.Duration(secs) * time.Second, true
	}

	when, terr := http.ParseTime(v)
	if terr != nil {
		return 0, false
	}

	d := when.Sub(now)
	if d <= 0 {
		return 0, false
	}

	if d > maxRA {
		return maxRA, true
	}

	return d, true
}
