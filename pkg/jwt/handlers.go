package jwt

// This file contains the net/http layer: login, renewal, and authorization
// handlers, the middleware, and the bearer-token request plumbing.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tecnickcom/nurago/pkg/httputil"
)

const (
	// headerWWWAuthenticate is the RFC 7235 challenge header set on 401 responses.
	headerWWWAuthenticate = "WWW-Authenticate"

	// challengeBearer is the RFC 6750 challenge sent when no bearer token was presented.
	challengeBearer = "Bearer"

	// challengeInvalidToken is the RFC 6750 challenge sent when a presented
	// bearer token was rejected.
	challengeInvalidToken = `Bearer error="invalid_token"` //nolint:gosec // RFC 6750 challenge value, not a credential
)

// Bearer-token extraction and session errors.
var (
	// ErrMissingAuthHeader is returned when the Authorization header is absent.
	ErrMissingAuthHeader = errors.New("jwt: missing Authorization header")

	// ErrMissingToken is returned when the bearer token is absent or empty.
	ErrMissingToken = errors.New("jwt: missing bearer token")

	// ErrSessionExpired names the event when a renewal is refused because the
	// session exceeded the configured maximum lifetime. It is used for logging
	// and challenge selection only; no exported method returns it.
	ErrSessionExpired = errors.New("jwt: session lifetime exceeded")
)

// Credentials holds the user name and password from the request body.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// claimsCtxKey is the private context key under which [JWT.Middleware] stores
// the verified claims.
type claimsCtxKey struct{}

// ClaimsFromContext returns the verified claims stored in ctx by
// [JWT.Middleware], reporting whether they were present.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsCtxKey{}).(*Claims)

	return claims, ok
}

// LoginHandler authenticates credentials and returns a signed JWT.
//
// It expects a JSON body with username/password (capped at the configured max
// body size), verifies them via [VerifyCredentialsFn], and replies with a token
// on success. The caller is responsible for restricting the HTTP method. Its
// 401 response carries no WWW-Authenticate challenge: credentials travel in the
// JSON body, not in an HTTP authentication scheme.
func (c *JWT) LoginHandler(w http.ResponseWriter, r *http.Request) {
	var creds Credentials

	ctx := r.Context()

	// Guard against a nil Body (handlers can be invoked directly, e.g. in tests);
	// http.MaxBytesReader would otherwise panic on the first read/close.
	if r.Body == nil {
		r.Body = http.NoBody
	}

	r.Body = http.MaxBytesReader(w, r.Body, c.maxBodyBytes)

	defer func() {
		cerr := r.Body.Close()
		if cerr != nil {
			c.logger.ErrorContext(ctx, "error closing request body", slog.Any("error", cerr))
		}
	}()

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(&creds)
	if err != nil {
		c.sendDecodeError(ctx, w, err)

		return
	}

	// Reject trailing data after the JSON object (strictness parity with
	// DisallowUnknownFields): a well-formed request carries exactly one value.
	if dec.More() {
		c.sendResponseFn(ctx, w, http.StatusBadRequest, "invalid request body")
		c.logger.WarnContext(ctx, "trailing data after JWT login body")

		return
	}

	ok, err := c.verifyCredentialsFn(creds.Username, creds.Password)
	if err != nil {
		// A backend failure is a real server error, distinct from invalid credentials.
		c.sendResponseFn(ctx, w, http.StatusInternalServerError, "unable to verify credentials")
		c.logger.ErrorContext(ctx, "credential verification failed",
			slog.String("username", creds.Username),
			slog.Any("error", err),
		)

		return
	}

	if !ok {
		// Uniform message for unknown user and wrong password (no account enumeration).
		c.sendResponseFn(ctx, w, http.StatusUnauthorized, "invalid authentication credentials")
		c.logger.WarnContext(ctx, "invalid JWT credentials", slog.String("username", creds.Username))

		return
	}

	c.sendTokenResponse(w, r, c.newClaims(creds.Username, nil))
}

// RenewHandler renews a valid token when it is close to expiration.
//
// Requests are rejected if the token is invalid, still outside the renew window
// configured by renewTime, or past the configured maximum session lifetime. The
// caller is responsible for restricting the HTTP method.
//
// Renewal does not invalidate the presented token: being stateless, it stays
// valid until its own expiration. Likewise, when the maximum session lifetime
// is exceeded only the renewal is refused; the presented token still authorizes
// requests until it expires.
func (c *JWT) RenewHandler(w http.ResponseWriter, r *http.Request) {
	claims, err := c.checkToken(r)
	if err != nil {
		c.writeUnauthorized(w, r, err)
		c.logger.WarnContext(r.Context(), "invalid JWT token",
			slog.String("username", claims.Username),
			slog.Any("error", err),
		)

		return
	}

	if time.Until(claims.ExpiresAt.Time) > c.renewTime {
		c.sendResponseFn(r.Context(), w, http.StatusBadRequest, "the JWT token can be renewed only when it is close to expiration")
		c.logger.InfoContext(r.Context(), "the JWT token cannot be renewed yet", slog.String("username", claims.Username))

		return
	}

	// Resolve the original session start: auth_time, falling back to iat for
	// tokens issued before auth_time was introduced.
	sessionStart := claims.AuthTime
	if sessionStart == nil {
		sessionStart = claims.IssuedAt
	}

	// Enforce an absolute session lifetime (when configured) so a stolen token
	// cannot be kept alive indefinitely by repeated renewals.
	if c.maxSessionLifetime > 0 &&
		(sessionStart == nil || time.Since(sessionStart.Time) > c.maxSessionLifetime) {
		c.writeUnauthorized(w, r, ErrSessionExpired)
		c.logger.WarnContext(r.Context(), "JWT session lifetime exceeded", slog.String("username", claims.Username))

		return
	}

	// Issue a token with fresh registered claims (exp, iat, nbf, jti) but the
	// preserved session start, so the renewed token extends expiration without
	// resetting the session clock.
	renewed := c.newClaims(claims.Username, sessionStart)

	// A renewal can yield a token that, after exp is truncated to whole seconds at
	// signing, is already expired. Refuse it rather than returning 200 with a
	// token that fails on first use.
	if c.expiredOnIssue(renewed.ExpiresAt) {
		c.writeUnauthorized(w, r, ErrSessionExpired)
		c.logger.WarnContext(r.Context(), c.renewRefusalReason(), slog.String("username", claims.Username))

		return
	}

	c.sendTokenResponse(w, r, renewed)
}

// renewRefusalReason describes why a born-expired renewal is refused, for logging.
// With a session cap configured the renewal has reached that cap; without one, the
// token would only be born-expired from whole-second `exp` truncation at a very
// short expiration.
func (c *JWT) renewRefusalReason() string {
	if c.maxSessionLifetime > 0 {
		return "JWT session lifetime exceeded"
	}

	return "renewed JWT token would be immediately expired"
}

// IsAuthorized validates the bearer token on the incoming request.
//
// On failure it writes an unauthorized response and returns false. Use
// [JWT.Authenticate] or [JWT.Middleware] when the verified claims are needed.
func (c *JWT) IsAuthorized(w http.ResponseWriter, r *http.Request) bool {
	claims, err := c.checkToken(r)
	if err != nil {
		c.writeUnauthorized(w, r, err)
		c.logger.WarnContext(r.Context(), "unauthorized JWT request",
			slog.String("username", claims.Username),
			slog.Any("error", err),
		)

		return false
	}

	return true
}

// Authenticate validates the bearer token on r and returns its verified claims.
//
// Unlike [JWT.IsAuthorized] it writes nothing to a [http.ResponseWriter], so it
// can be used inside custom middleware. The returned error is for logging or
// branching; do not echo it to clients (it may reveal token-validation
// internals). The returned claims are never nil; they are populated only when
// the token signature verified.
func (c *JWT) Authenticate(r *http.Request) (*Claims, error) {
	return c.checkToken(r)
}

// Middleware wraps next, rejecting requests without a valid bearer token and
// injecting the verified claims into the request context for retrieval via
// [ClaimsFromContext]. Failure responses and logging match [JWT.IsAuthorized].
func (c *JWT) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := c.checkToken(r)
		if err != nil {
			c.writeUnauthorized(w, r, err)
			c.logger.WarnContext(r.Context(), "unauthorized JWT request",
				slog.String("username", claims.Username),
				slog.Any("error", err),
			)

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsCtxKey{}, claims)))
	})
}

// sendTokenResponse signs claims and writes the token response.
//
// If signing fails, it returns a 500 response with a generic error message.
func (c *JWT) sendTokenResponse(w http.ResponseWriter, r *http.Request, claims *Claims) {
	signedToken, err := c.signToken(claims)
	if err != nil {
		c.sendResponseFn(r.Context(), w, http.StatusInternalServerError, "unable to sign the JWT token")
		c.logger.ErrorContext(r.Context(), "unable to sign the JWT token",
			slog.String("username", claims.Username),
			slog.Any("error", err),
		)

		return
	}

	c.sendResponseFn(r.Context(), w, http.StatusOK, signedToken)
}

// sendDecodeError maps a login body decode failure to an HTTP response. An
// oversize body is reported as 413; any other decode error as a generic 400. The
// parser detail is kept in the server log only, so nothing internal is leaked.
func (c *JWT) sendDecodeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, msg := http.StatusBadRequest, "invalid request body"

	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		status, msg = http.StatusRequestEntityTooLarge, "request body too large"
	}

	c.sendResponseFn(ctx, w, status, msg)
	c.logger.WarnContext(ctx, "invalid JWT login body", slog.Any("error", err))
}

// writeUnauthorized writes a 401 response carrying the RFC 6750 Bearer
// challenge: a bare challenge when no bearer token was presented, and
// error="invalid_token" when a presented token was rejected.
func (c *JWT) writeUnauthorized(w http.ResponseWriter, r *http.Request, err error) {
	challenge := challengeInvalidToken
	if errors.Is(err, ErrMissingAuthHeader) || errors.Is(err, ErrMissingToken) {
		challenge = challengeBearer
	}

	w.Header().Set(headerWWWAuthenticate, challenge)
	c.sendResponseFn(r.Context(), w, http.StatusUnauthorized, "unauthorized")
}

// checkToken extracts the bearer token from the authorization header and
// parses, verifies, and validates it. The returned claims are never nil; they
// are populated only when the token signature verified.
func (c *JWT) checkToken(r *http.Request) (*Claims, error) {
	headAuth := r.Header.Get(c.authorizationHeader)
	if len(headAuth) == 0 {
		return &Claims{}, ErrMissingAuthHeader
	}

	// RFC 7235 §2.1: authentication scheme names are case-insensitive and may
	// be separated from the credentials by more than one space.
	prefixLen := len(httputil.HeaderAuthBearer)
	if len(headAuth) <= prefixLen || !strings.EqualFold(headAuth[:prefixLen], httputil.HeaderAuthBearer) {
		return &Claims{}, ErrMissingToken
	}

	signedToken := strings.TrimLeft(headAuth[prefixLen:], " ")
	if signedToken == "" {
		return &Claims{}, ErrMissingToken
	}

	return c.parseToken(signedToken)
}
