/*
Package jwt provides an HTTP-oriented JWT authentication helper for
username/password login flows: validate user credentials, issue short-lived
signed JWTs, authorize protected endpoints from an Authorization header, and
optionally renew tokens near expiration.

The API is compatible with net/http:

  - [JWT.LoginHandler]: validates credentials and issues signed JWTs.
  - [JWT.RenewHandler]: renews a valid token only when it is close to expiry.
  - [JWT.IsAuthorized]: validates bearer tokens for protected handlers.
  - [JWT.Middleware]: wraps a handler, injecting verified claims into the
    request context for retrieval via [ClaimsFromContext].
  - [JWT.Authenticate]: validates a bearer token and returns its claims, for
    building custom middleware.
  - [JWT.IssueToken]: mints a token outside the HTTP login flow, once the
    caller has verified the user's identity by its own means.
  - [JWT.VerifyToken]: validates a raw token string that arrived over any
    transport (WebSocket messages, queue payloads, gRPC metadata).

Credential verification is delegated to a caller-provided
[VerifyCredentialsFn], so the package is agnostic to the password-hashing
scheme; use the OWASP-compliant github.com/tecnickcom/nurago/pkg/passwordhash
(Argon2id) for storage.

# Implementation

Tokens are RFC 7515 compact JWS with RFC 7519 claims, signed with HMAC-SHA2
(RFC 7518 §3.2: HS256, HS384 or HS512). The implementation is self-contained on
the Go standard library. Restricting the surface to
symmetric HMAC makes the classic JWT attacks (alg=none, asymmetric-to-HMAC
confusion) structurally impossible: the accepted algorithm is pinned and the
signature is verified before the claims payload is ever decoded. A `crit` header
(RFC 7515 §4.1.11) or a duplicated header parameter is rejected; other unknown
JOSE header parameters (typ, kid, ...) are ignored. The `exp` and `nbf` time
claims are validated (with optional leeway); `iat` is decoded but not validated,
as only a key holder could forge it.

# Authentication Flow

 1. The login endpoint decodes JSON credentials (`username`, `password`).
 2. A user-provided [VerifyCredentialsFn] checks them against the user store.
 3. On success, a JWT is signed with configured claims and returned as text.
 4. Downstream handlers validate `Authorization: Bearer <token>` via
    [JWT.IsAuthorized], [JWT.Middleware], [JWT.Authenticate], or
    [JWT.RenewHandler].

# Claims and Defaults

By default the package issues short-lived HMAC-signed tokens with:
  - expiration: [DefaultExpirationTime] (5 minutes)
  - renew window: [DefaultRenewTime] (30 seconds before expiry)
  - header name: [DefaultAuthorizationHeader] (`Authorization`)
  - request body cap: [DefaultMaxBodyBytes]
  - token size cap: [DefaultMaxTokenBytes]

Issued tokens include standard registered claims (`exp`, `iat`, `nbf`, `jti`,
`sub`) and an `auth_time` claim recording the original login. They support
optional `iss` and `aud` via options. The `sub` (Subject) claim is set to the
authenticated username. When `iss` and/or `aud` are configured, they are also
enforced during verification: a token missing them, or carrying different
values, is rejected.

# Extension Points

Functional options allow custom behavior without replacing core handlers:
  - response output customization ([WithSendResponseFn])
  - token/header settings ([WithExpirationTime], [WithRenewTime],
    [WithAuthorizationHeader], [WithSigningMethod], [WithMaxBodyBytes],
    [WithMaxTokenBytes])
  - session controls ([WithMaxSessionLifetime], [WithClockSkewLeeway])
  - key rotation ([WithPreviousKeys])
  - claim metadata ([WithClaimIssuer], [WithClaimAudience])
  - logger customization ([WithLogger])

# Security Notes

  - Only HMAC signing methods (HS256/HS384/HS512) are supported: the same
    symmetric key both signs and verifies. Keep it secret, and at least as long
    as the signing method's hash output (enforced by [New]).
  - To rotate the signing key without invalidating outstanding sessions, deploy
    the new key while listing the old one in [WithPreviousKeys], then drop the
    old key once the rotation window (expiration time plus renew window) has
    elapsed.
  - Return uniform error messages for invalid credentials to avoid account
    enumeration (the default login path already does this). A
    [VerifyCredentialsFn] MUST also equalize its own timing between known and
    unknown users so existence does not leak through response latency.
  - Use HTTPS so bearer tokens are never exposed in transit.
  - The handlers do not restrict the HTTP method; the caller is responsible for
    routing login to POST and protected endpoints appropriately.
  - Tokens are stateless: there is no server-side revocation before `exp`, and
    renewing a token does not invalidate the previous one, which stays valid
    until its own expiration. Configure short expiration windows appropriate
    for your threat model, and bound how long a session may be kept alive by
    renewals with [WithMaxSessionLifetime].
  - The package does not rate-limit or lock out repeated failed logins;
    brute-force protection (rate limiting, lockout, CAPTCHA) must be layered by
    the caller.
  - The default responder logs the full response body, including the issued
    token, at debug level. Where debug logs are retained, disable them or pass a
    redacting logger via [WithLogger] (see github.com/tecnickcom/nurago/pkg/redact,
    which detects JWT compact serializations).

# When To Use

  - A service authenticates with username and password and issues short-lived
    tokens.
  - Protected endpoints authorize from the Authorization header.
  - Tokens should be renewable near expiry without a fresh login.
*/
package jwt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/random"
)

const (
	// DefaultExpirationTime is the default JWT expiration time.
	DefaultExpirationTime = 5 * time.Minute

	// DefaultRenewTime is the default time before the JWT expiration when the renewal is allowed.
	DefaultRenewTime = 30 * time.Second

	// DefaultAuthorizationHeader is the default authorization header name.
	DefaultAuthorizationHeader = httputil.HeaderAuthorization

	// DefaultMaxBodyBytes is the default maximum accepted size, in bytes, of a
	// login request body. It is generous for JSON credentials while capping the
	// memory an unauthenticated caller can force the login handler to allocate.
	DefaultMaxBodyBytes int64 = 1 << 13 // 8 KiB

	// DefaultMaxTokenBytes bounds a compact JWS, both when accepted for
	// verification and when minted. A legitimate token carrying the registered
	// claims and a short username is well under 1 KiB; the cap keeps a caller from
	// forcing a large base64 and JSON decode of the JOSE header, which must
	// necessarily be parsed before the signature can be checked. The username is
	// not otherwise bounded, so an unusually long one can push a minted token past
	// this cap, in which case signing fails with [ErrTokenTooLarge]; raise the cap
	// with [WithMaxTokenBytes] for long usernames or large custom claims.
	DefaultMaxTokenBytes int = 1 << 13 // 8 KiB
)

// Configuration and validation errors returned by [New].
var (
	// ErrEmptyKey is returned when the signing key is empty.
	ErrEmptyKey = errors.New("jwt: empty signing key")

	// ErrWeakKey is returned when a signing or verification key is shorter than
	// the signing method's hash output size (RFC 7518 §3.2). The wrapped message
	// names which key (the signing key, or a previous key by index) is too short.
	ErrWeakKey = errors.New("jwt: key is shorter than the signing method hash size")

	// ErrNilVerifyFn is returned when no credential verification function is provided.
	ErrNilVerifyFn = errors.New("jwt: credential verification function is required")

	// ErrInvalidSigningMethod is returned when the signing method is not one of
	// the supported constants.
	ErrInvalidSigningMethod = errors.New("jwt: invalid signing method")

	// ErrInvalidExpirationTime is returned when the expiration time is not positive.
	ErrInvalidExpirationTime = errors.New("jwt: expiration time must be positive")

	// ErrShortExpirationTime is returned when the expiration time is positive but
	// shorter than one second. Token times are stamped at whole-second resolution,
	// so a sub-second expiration cannot be represented and would mint tokens that
	// expire the instant they are issued.
	ErrShortExpirationTime = errors.New("jwt: expiration time must be at least one second")

	// ErrInvalidRenewTime is returned when the renew time is not positive.
	ErrInvalidRenewTime = errors.New("jwt: renew time must be positive")

	// ErrInvalidMaxBodyBytes is returned when the max body size is not positive.
	ErrInvalidMaxBodyBytes = errors.New("jwt: max body bytes must be positive")

	// ErrInvalidMaxTokenBytes is returned when the max token size is not positive.
	ErrInvalidMaxTokenBytes = errors.New("jwt: max token bytes must be positive")

	// ErrInvalidMaxSessionLifetime is returned when the max session lifetime is negative.
	ErrInvalidMaxSessionLifetime = errors.New("jwt: max session lifetime must not be negative")

	// ErrShortMaxSessionLifetime is returned when the max session lifetime is
	// positive but shorter than one second. Token times are stamped at
	// whole-second resolution, so a sub-second cap cannot be represented and would
	// mint tokens that expire the instant they are issued.
	ErrShortMaxSessionLifetime = errors.New("jwt: max session lifetime must be at least one second")

	// ErrInvalidClockSkewLeeway is returned when the clock-skew leeway is negative.
	ErrInvalidClockSkewLeeway = errors.New("jwt: clock skew leeway must not be negative")
)

// SendResponseFn is the type of function used to send back the HTTP responses.
type SendResponseFn func(ctx context.Context, w http.ResponseWriter, statusCode int, data string)

// VerifyCredentialsFn verifies a username/password pair against the user store.
//
// It returns:
//   - (true, nil)  when the credentials are valid;
//   - (false, nil) when the username is unknown or the password is wrong;
//   - (false, err) when verification could not be completed because of a
//     backend failure (e.g. the user store is unreachable). The login handler
//     reports this as 500, distinct from the 401 returned for invalid
//     credentials, so real outages are not masked as authentication failures.
//
// Implementations MUST NOT leak account existence through response timing:
// perform comparable work for unknown and known users (for example, verify the
// password against a fixed decoy hash when the user does not exist). See
// github.com/tecnickcom/nurago/pkg/passwordhash for a constant-time,
// OWASP-compliant verifier.
type VerifyCredentialsFn func(username, password string) (bool, error)

// JWT provides HTTP handlers and helpers for token-based authentication.
type JWT struct {
	key                 []byte              // JWT signing key.
	previousKeys        [][]byte            // Previous signing keys still accepted for verification.
	verifyKeys          [][]byte            // Precomputed verification keys: current key first, then previous ones.
	encodedHeader       string              // Precomputed base64url JOSE header for the configured method.
	expirationTime      time.Duration       // JWT expiration time.
	renewTime           time.Duration       // Time before the JWT expiration when the renewal is allowed.
	maxSessionLifetime  time.Duration       // Absolute cap on how long a session may be renewed (0 = unlimited).
	clockSkewLeeway     time.Duration       // Allowed clock-skew leeway during verification (0 = none).
	maxBodyBytes        int64               // Maximum accepted login request body size in bytes.
	maxTokenBytes       int                 // Maximum accepted compact-JWS token size in bytes.
	sendResponseFn      SendResponseFn      // Response function used to send back the HTTP responses.
	verifyCredentialsFn VerifyCredentialsFn // Function used to verify user credentials.
	signingMethod       SigningMethod       // HMAC signing method.
	authorizationHeader string
	issuer              string             // the `iss` (Issuer) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.1
	audience            []string           // the `aud` (Audience) claim. See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.3
	logger              *slog.Logger       // Structured logger.
	httpresp            *httputil.HTTPResp // HTTP response helper.
	rnd                 *random.Rnd        // Random source for the `jti` claim.
}

// defaultJWT returns a JWT instance initialized with package defaults.
//
// It wires default expiration, renew window, authorization header, body cap,
// signing method, logger, and response helpers.
func defaultJWT() *JWT {
	cfg := &JWT{
		expirationTime:      DefaultExpirationTime,
		renewTime:           DefaultRenewTime,
		maxBodyBytes:        DefaultMaxBodyBytes,
		maxTokenBytes:       DefaultMaxTokenBytes,
		authorizationHeader: DefaultAuthorizationHeader,
		signingMethod:       SigningMethodHS256,
		logger:              slog.Default(),
		rnd:                 random.New(nil),
	}

	cfg.sendResponseFn = cfg.defaultSendResponse

	return cfg
}

// New constructs a JWT authentication helper.
//
// It applies options, restores defaults for any optional setting an option may
// have cleared, validates the configuration (signing keys, credential verifier,
// signing method, positive durations and body cap), precomputes the token
// signing and verification material, and prepares HTTP response utilities.
//
// The signing key and any previous keys are copied, so later mutation of the
// caller's buffers does not affect the instance.
func New(key []byte, verifyFn VerifyCredentialsFn, opts ...Option) (*JWT, error) {
	c := defaultJWT()
	c.key = key
	c.verifyCredentialsFn = verifyFn

	for _, applyOpt := range opts {
		applyOpt(c)
	}

	c.normalizeOptionals()

	err := c.validate()
	if err != nil {
		return nil, err
	}

	c.initTokenConfig()
	c.httpresp = httputil.NewHTTPResp(c.logger)

	return c, nil
}

// normalizeOptionals restores defaults for optional settings that an option may
// have cleared to a nil/empty value, so handlers never panic on a nil logger or
// responder and an empty header name does not silently disable authentication.
func (c *JWT) normalizeOptionals() {
	if c.logger == nil {
		c.logger = slog.Default()
	}

	if c.sendResponseFn == nil {
		c.sendResponseFn = c.defaultSendResponse
	}

	if c.authorizationHeader == "" {
		c.authorizationHeader = DefaultAuthorizationHeader
	}
}

// validate checks that the configuration is complete and safe.
func (c *JWT) validate() error {
	checks := []struct {
		invalid bool
		err     error
	}{
		{len(c.key) == 0, ErrEmptyKey},
		{c.verifyCredentialsFn == nil, ErrNilVerifyFn},
		{c.signingMethod.Alg() == "", ErrInvalidSigningMethod},
		{c.expirationTime <= 0, ErrInvalidExpirationTime},
		{c.expirationTime > 0 && c.expirationTime < time.Second, ErrShortExpirationTime},
		{c.renewTime <= 0, ErrInvalidRenewTime},
		{c.maxBodyBytes <= 0, ErrInvalidMaxBodyBytes},
		{c.maxTokenBytes <= 0, ErrInvalidMaxTokenBytes},
		{c.maxSessionLifetime < 0, ErrInvalidMaxSessionLifetime},
		{c.maxSessionLifetime > 0 && c.maxSessionLifetime < time.Second, ErrShortMaxSessionLifetime},
		{c.clockSkewLeeway < 0, ErrInvalidClockSkewLeeway},
	}

	for _, check := range checks {
		if check.invalid {
			return check.err
		}
	}

	// Every HMAC key must be at least as long as the hash output (RFC 7518
	// §3.2): the signing key and any previous verification keys alike.
	minLen := c.signingMethod.hashSize()
	if len(c.key) < minLen {
		return fmt.Errorf("%w: signing key is %d bytes, need at least %d", ErrWeakKey, len(c.key), minLen)
	}

	for i, key := range c.previousKeys {
		if len(key) < minLen {
			return fmt.Errorf("%w: previous key %d is %d bytes, need at least %d", ErrWeakKey, i, len(key), minLen)
		}
	}

	return nil
}

// defaultSendResponse writes plain-text HTTP responses via httputil.HTTPResp.
func (c *JWT) defaultSendResponse(ctx context.Context, w http.ResponseWriter, statusCode int, data string) {
	c.httpresp.SendText(ctx, w, statusCode, data)
}
