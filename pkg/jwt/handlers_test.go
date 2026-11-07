package jwt

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/testutil"
)

//nolint:gocognit
func TestLoginHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		body         string
		want         string
		status       int
		verifyFn     VerifyCredentialsFn
		maxBodyBytes int64
		breakSigning bool
		closeError   bool
		nilBody      bool
	}{
		{
			name:   "fails with empty body",
			want:   "invalid request body",
			status: http.StatusBadRequest,
		},
		{
			name:    "fails with nil body",
			want:    "invalid request body",
			status:  http.StatusBadRequest,
			nilBody: true,
		},
		{
			name:   "fails with invalid body",
			body:   `{"broken":"...`,
			want:   "invalid request body",
			status: http.StatusBadRequest,
		},
		{
			name:   "fails with unknown field",
			body:   `{"username":"test-name", "password":"test-name", "extra":"x"}`,
			want:   "invalid request body",
			status: http.StatusBadRequest,
		},
		{
			name:         "fails with oversize body",
			body:         `{"username":"test-name", "password":"test-name"}`,
			want:         "request body too large",
			status:       http.StatusRequestEntityTooLarge,
			maxBodyBytes: 8,
		},
		{
			name:   "fails with trailing data",
			body:   `{"username":"test-name", "password":"test-name"}{"extra":1}`,
			want:   "invalid request body",
			status: http.StatusBadRequest,
		},
		{
			name:   "fails with invalid username",
			body:   `{"username":"", "password":"test-secret"}`,
			want:   "invalid authentication credentials",
			status: http.StatusUnauthorized,
		},
		{
			name:   "fails with empty password",
			body:   `{"username":"test-name", "password":""}`,
			want:   "invalid authentication credentials",
			status: http.StatusUnauthorized,
		},
		{
			name:   "fails with invalid password",
			body:   `{"username":"test-name", "password":"invalid-password"}`,
			want:   "invalid authentication credentials",
			status: http.StatusUnauthorized,
		},
		{
			name:     "fails with backend error",
			body:     `{"username":"test-name", "password":"test-name"}`,
			want:     "unable to verify credentials",
			status:   http.StatusInternalServerError,
			verifyFn: func(_, _ string) (bool, error) { return false, errors.New("backend down") },
		},
		{
			name:         "fails with signing error",
			body:         `{"username":"test-name", "password":"test-name"}`,
			want:         "unable to sign the JWT token",
			status:       http.StatusInternalServerError,
			breakSigning: true,
		},
		{
			name:   "success",
			body:   `{"username":"test-name", "password":"test-name"}`,
			status: http.StatusOK,
		},
		{
			name:       "close error",
			body:       `{"username":"test-name", "password":"test-name"}`,
			want:       "invalid request body",
			status:     http.StatusBadRequest,
			closeError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []Option

			if tt.maxBodyBytes > 0 {
				opts = append(opts, WithMaxBodyBytes(tt.maxBodyBytes))
			}

			verifyFn := tt.verifyFn
			if verifyFn == nil {
				verifyFn = testVerify
			}

			c, err := New(testKey, verifyFn, opts...)
			require.NotNil(t, c)
			require.NoError(t, err)

			if tt.breakSigning {
				// White-box: corrupt the signing method after construction to
				// exercise the signing-failure response path.
				c.signingMethod = SigningMethod(99)
			}

			rr := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", strings.NewReader(tt.body))

			if tt.closeError {
				req.Body = testutil.NewErrorCloser("close error")
			}

			if tt.nilBody {
				req.Body = nil
			}

			c.LoginHandler(rr, req)

			resp := rr.Result()
			require.NotNil(t, resp)

			defer func() {
				err := resp.Body.Close()
				require.NoError(t, err, "error closing resp.Body")
			}()

			body, _ := io.ReadAll(resp.Body)

			require.Equal(t, tt.status, resp.StatusCode)

			if tt.status != http.StatusOK {
				require.Equal(t, tt.want, string(body))
			} else {
				require.Greater(t, len(body), 100)
			}
		})
	}
}

func TestRenewHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		status              int
		expirationTime      time.Duration
		authorizationHeader string
		bearerHeader        string
		badToken            bool
	}{
		{
			name:                "unauthorized",
			status:              http.StatusUnauthorized,
			expirationTime:      1 * time.Second,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        httputil.HeaderAuthBearer,
			badToken:            true,
		},
		{
			name:                "wrong authorization header",
			status:              http.StatusUnauthorized,
			expirationTime:      1 * time.Second,
			authorizationHeader: "ERROR",
			bearerHeader:        httputil.HeaderAuthBearer,
		},
		{
			name:                "wrong authorization value",
			status:              http.StatusUnauthorized,
			expirationTime:      1 * time.Second,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        "ERROR",
		},
		{
			name:                "too early",
			status:              http.StatusBadRequest,
			expirationTime:      5 * time.Second,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        httputil.HeaderAuthBearer,
		},
		{
			name:                "success",
			status:              http.StatusOK,
			expirationTime:      1 * time.Second,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        httputil.HeaderAuthBearer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(
				testKey,
				testVerify,
				WithExpirationTime(tt.expirationTime),
				WithRenewTime(1*time.Second),
			)
			require.NotNil(t, c)
			require.NoError(t, err)

			reqBody := `{"username":"test-name", "password":"test-name"}`

			rr := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", strings.NewReader(reqBody))
			c.LoginHandler(rr, req)

			resp := rr.Result()
			require.NotNil(t, resp)

			defer func() {
				err := resp.Body.Close()
				require.NoError(t, err, "error closing resp.Body")
			}()

			require.Equal(t, http.StatusOK, resp.StatusCode)

			token, _ := io.ReadAll(resp.Body)

			rr2 := httptest.NewRecorder()
			req2, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

			header := tt.bearerHeader + string(token)

			if tt.badToken {
				header += "CORRUPT"
			}

			req2.Header.Set(tt.authorizationHeader, header)
			c.RenewHandler(rr2, req2)

			resp2 := rr2.Result()
			require.NotNil(t, resp2)

			defer func() {
				err := resp2.Body.Close()
				require.NoError(t, err, "error closing resp2.Body")
			}()

			require.Equal(t, tt.status, resp2.StatusCode)
		})
	}
}

func TestRenewHandlerIssuesFreshToken(t *testing.T) {
	t.Parallel()

	c, err := New(
		testKey,
		testVerify,
		WithExpirationTime(5*time.Second),
		WithRenewTime(5*time.Second),
	)
	require.NotNil(t, c)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", strings.NewReader(`{"username":"test-name", "password":"test-name"}`))
	c.LoginHandler(rr, req)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	oldToken, _ := io.ReadAll(resp.Body)

	// Wait past a full second so the renewed exp (second-precision) is strictly later.
	time.Sleep(1100 * time.Millisecond)

	rr2 := httptest.NewRecorder()
	req2, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req2.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+string(oldToken))
	c.RenewHandler(rr2, req2)

	resp2 := rr2.Result()
	require.NotNil(t, resp2)

	defer func() {
		err := resp2.Body.Close()
		require.NoError(t, err, "error closing resp2.Body")
	}()

	require.Equal(t, http.StatusOK, resp2.StatusCode)

	newToken, _ := io.ReadAll(resp2.Body)

	// The renewed token must not be a re-signed copy of the old one.
	require.NotEqual(t, string(oldToken), string(newToken))

	oldClaims, err := c.parseToken(string(oldToken))
	require.NoError(t, err)

	newClaims, err := c.parseToken(string(newToken))
	require.NoError(t, err)

	require.True(t, newClaims.ExpiresAt.After(oldClaims.ExpiresAt.Time), "renewed token must expire later than the original")
	require.NotEqual(t, oldClaims.ID, newClaims.ID, "renewed token must have a fresh jti")
	require.Equal(t, oldClaims.Username, newClaims.Username)
	require.Equal(t, "test-name", newClaims.Subject, "sub claim must be the authenticated username")

	require.NotNil(t, oldClaims.AuthTime)
	require.NotNil(t, newClaims.AuthTime)
	require.Equal(t, oldClaims.AuthTime.Unix(), newClaims.AuthTime.Unix(), "auth_time must be preserved across renewal")
}

func TestRenewHandlerSessionLifetime(t *testing.T) {
	t.Parallel()

	c, err := New(
		testKey,
		testVerify,
		WithMaxSessionLifetime(1*time.Hour),
		WithRenewTime(1*time.Minute),
	)
	require.NotNil(t, c)
	require.NoError(t, err)

	now := time.Now()
	past2h := now.Add(-2 * time.Hour).Unix()
	recent := now.Add(-10 * time.Minute).Unix()

	// mkToken crafts a token within the renew window with the given session-start
	// markers, so RenewHandler reaches the session-lifetime check.
	mkToken := func(authTime, issuedAt *int64) string {
		claims := jwtv5.MapClaims{
			"username": "test-name",
			"exp":      now.Add(30 * time.Second).Unix(),
		}

		if authTime != nil {
			claims["auth_time"] = *authTime
		}

		if issuedAt != nil {
			claims["iat"] = *issuedAt
		}

		return signClaimsV5(t, testKey, claims)
	}

	tests := []struct {
		name       string
		authTime   *int64
		issuedAt   *int64
		wantStatus int
	}{
		{"exceeded via auth_time", &past2h, &past2h, http.StatusUnauthorized},
		{"within cap via auth_time", &recent, &recent, http.StatusOK},
		{"fallback to iat within cap", nil, &recent, http.StatusOK},
		{"fallback to iat exceeded", nil, &past2h, http.StatusUnauthorized},
		{"no session start", nil, nil, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+mkToken(tt.authTime, tt.issuedAt))
			c.RenewHandler(rr, req)

			resp := rr.Result()
			require.NotNil(t, resp)

			defer func() {
				err := resp.Body.Close()
				require.NoError(t, err, "error closing resp.Body")
			}()

			require.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.wantStatus == http.StatusUnauthorized {
				require.Equal(t, challengeInvalidToken, resp.Header.Get(headerWWWAuthenticate),
					"session-cap 401 must carry the invalid_token challenge")
			}
		})
	}
}

func TestRenewHandlerClampsExpToSessionLifetime(t *testing.T) {
	t.Parallel()

	const maxLifetime = 30 * time.Minute

	// A renewal granted while still under the cap must not mint a token that
	// outlives auth_time + maxSessionLifetime. With expirationTime (10m) larger
	// than the remaining budget, an unclamped renewal would overshoot the cap.
	c, err := New(
		testKey,
		testVerify,
		WithExpirationTime(10*time.Minute),
		WithRenewTime(1*time.Minute),
		WithMaxSessionLifetime(maxLifetime),
	)
	require.NotNil(t, c)
	require.NoError(t, err)

	now := time.Now()
	authTime := now.Add(-29 * time.Minute) // 1 minute of session budget remains

	presented := signClaimsV5(t, testKey, jwtv5.MapClaims{
		"username":  "test-name",
		"exp":       now.Add(30 * time.Second).Unix(), // inside the renew window
		"auth_time": authTime.Unix(),
	})

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+presented)
	c.RenewHandler(rr, req)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	renewed, _ := io.ReadAll(resp.Body)

	claims, err := c.parseToken(string(renewed))
	require.NoError(t, err)

	require.LessOrEqual(t, claims.ExpiresAt.Unix(), authTime.Add(maxLifetime).Unix(),
		"renewed exp must not outlive auth_time + maxSessionLifetime")
	require.Equal(t, authTime.Unix(), claims.AuthTime.Unix(),
		"auth_time must be preserved across renewal")
}

func TestLoginHandlerRefusesOversizeToken(t *testing.T) {
	t.Parallel()

	// A long-but-accepted username fits under the body cap yet mints a token past
	// the token cap; issuance fails and surfaces the existing 500 sign-failure
	// path rather than a 200 with a token that would fail verification.
	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	// testVerify accepts when password == username; ~3 KiB each stays under the
	// 8 KiB body cap but produces a >8 KiB token.
	cred := strings.Repeat("a", 3000)
	body := `{"username":"` + cred + `","password":"` + cred + `"}`
	require.Less(t, len(body), int(DefaultMaxBodyBytes), "body must fit under the body cap")

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
	c.LoginHandler(rr, req)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"a login whose token would exceed the cap must fail rather than 200 with a dead token")
}

func TestRenewHandlerRefusesBornExpiredAtCap(t *testing.T) {
	t.Parallel()

	// A fractional-second cap: a renewal can be allowed by the cap gate (real
	// sub-second budget remains) yet mint a token whose exp, floored to whole
	// seconds, is already in the past. That renewal must be refused as
	// session-exceeded (401), not returned as a 200 carrying a dead token.
	c, err := New(
		testKey,
		testVerify,
		WithExpirationTime(time.Hour),
		WithRenewTime(time.Hour),
		WithMaxSessionLifetime(1900*time.Millisecond),
	)
	require.NotNil(t, c)
	require.NoError(t, err)

	// Wait for a sub-second phase where the cap gate still allows the renewal, so
	// the born-expired guard (not the gate) is what refuses it.
	for {
		if f := float64(time.Now().Nanosecond()) / 1e9; f > 0.05 && f < 0.55 {
			break
		}
	}

	now := time.Now()
	authSec := now.Truncate(time.Second).Add(-time.Second).Unix() // floor(now) - 1s

	presented := signClaimsV5(t, testKey, jwtv5.MapClaims{
		"username":  "test-name",
		"exp":       now.Add(30 * time.Second).Unix(), // valid, inside the renew window
		"auth_time": authSec,
	})

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+presented)
	c.RenewHandler(rr, req)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"a renewal that would mint a born-expired token must be refused")
	require.Equal(t, challengeInvalidToken, resp.Header.Get(headerWWWAuthenticate),
		"the refusal must carry the invalid_token challenge")
}

func TestRenewRefusalReason(t *testing.T) {
	t.Parallel()

	// With a session cap the born-expired refusal is a genuine cap overrun; without
	// one it is only the whole-second exp-truncation edge, so the log must not claim
	// a session lifetime was exceeded.
	capped, err := New(testKey, testVerify, WithMaxSessionLifetime(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "JWT session lifetime exceeded", capped.renewRefusalReason())

	uncapped, err := New(testKey, testVerify)
	require.NoError(t, err)
	require.Equal(t, "renewed JWT token would be immediately expired", uncapped.renewRefusalReason())
}

func TestIsAuthorized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		status              int
		authorizationHeader string
		bearerHeader        string
		badToken            bool
		wantChallenge       string
	}{
		{
			name:                "unauthorized",
			status:              http.StatusUnauthorized,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        httputil.HeaderAuthBearer,
			badToken:            true,
			wantChallenge:       challengeInvalidToken,
		},
		{
			name:                "wrong authorization header",
			status:              http.StatusUnauthorized,
			authorizationHeader: "ERROR",
			bearerHeader:        httputil.HeaderAuthBearer,
			wantChallenge:       challengeBearer,
		},
		{
			name:                "wrong authorization value",
			status:              http.StatusUnauthorized,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        "ERROR",
			wantChallenge:       challengeBearer,
		},
		{
			name:                "success",
			status:              0,
			authorizationHeader: DefaultAuthorizationHeader,
			bearerHeader:        httputil.HeaderAuthBearer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(
				testKey,
				testVerify,
			)
			require.NotNil(t, c)
			require.NoError(t, err)

			reqBody := `{"username":"test-name", "password":"test-name"}`

			rr := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", strings.NewReader(reqBody))
			c.LoginHandler(rr, req)

			resp := rr.Result()
			require.NotNil(t, resp)

			defer func() {
				err := resp.Body.Close()
				require.NoError(t, err, "error closing resp.Body")
			}()

			require.Equal(t, http.StatusOK, resp.StatusCode)

			token, _ := io.ReadAll(resp.Body)

			rr2 := httptest.NewRecorder()
			req2, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

			header := tt.bearerHeader + string(token)

			if tt.badToken {
				header += "CORRUPT"
			}

			req2.Header.Set(tt.authorizationHeader, header)
			got := c.IsAuthorized(rr2, req2)

			if tt.status == 0 {
				require.True(t, got)

				return
			}

			resp2 := rr2.Result()
			require.NotNil(t, resp2)

			defer func() {
				err := resp2.Body.Close()
				require.NoError(t, err, "error closing resp2.Body")
			}()

			require.Equal(t, tt.status, resp2.StatusCode)
			require.Equal(t, tt.wantChallenge, resp2.Header.Get(headerWWWAuthenticate),
				"401 must carry the RFC 6750 WWW-Authenticate challenge")
		})
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	// A valid token yields its verified claims and no error.
	signedToken := signClaimsV5(t, testKey, jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
	})

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signedToken)

	claims, err := c.Authenticate(req)
	require.NoError(t, err)
	require.Equal(t, "test-name", claims.Username)

	// A request without a token returns an error and writes nothing.
	reqBad, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	got, err := c.Authenticate(reqBad)
	require.NotNil(t, got)
	require.ErrorIs(t, err, ErrMissingAuthHeader)
}

func TestMiddleware(t *testing.T) {
	t.Parallel()

	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	var (
		nextCalled bool
		gotClaims  *Claims
		gotOK      bool
	)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims, gotOK = ClaimsFromContext(r.Context())
		nextCalled = true

		w.WriteHeader(http.StatusOK)
	})

	handler := c.Middleware(next)

	// A valid token reaches the wrapped handler with claims in context.
	signedToken := signClaimsV5(t, testKey, jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
	})

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signedToken)
	handler.ServeHTTP(rr, req)

	require.True(t, nextCalled)
	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, gotOK, "claims must be available from the request context")
	require.NotNil(t, gotClaims)
	require.Equal(t, "test-name", gotClaims.Username)

	// A request without a token is rejected with the challenge and the wrapped
	// handler is not invoked.
	nextCalled = false

	rr2 := httptest.NewRecorder()
	req2, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(rr2, req2)

	require.False(t, nextCalled)
	require.Equal(t, http.StatusUnauthorized, rr2.Code)
	require.Equal(t, challengeBearer, rr2.Header().Get(headerWWWAuthenticate))
}

func TestClaimsFromContextMissing(t *testing.T) {
	t.Parallel()

	claims, ok := ClaimsFromContext(t.Context())
	require.False(t, ok)
	require.Nil(t, claims)
}

func TestNewContextWithClaims(t *testing.T) {
	t.Parallel()

	want := &Claims{Subject: "alice"}

	got, ok := ClaimsFromContext(NewContextWithClaims(t.Context(), want))
	require.True(t, ok)
	require.Same(t, want, got)
}

func TestNewContextWithClaimsNil(t *testing.T) {
	t.Parallel()

	claims, ok := ClaimsFromContext(NewContextWithClaims(t.Context(), nil))
	require.False(t, ok)
	require.Nil(t, claims)

	outer := NewContextWithClaims(t.Context(), &Claims{Subject: "alice"})

	claims, ok = ClaimsFromContext(NewContextWithClaims(outer, nil))
	require.False(t, ok, "nil claims hide the outer claims")
	require.Nil(t, claims)
}

func TestPreviousKeysRotation(t *testing.T) {
	t.Parallel()

	oldKey := []byte("old-key-89abcdef0123456789abcdef")
	unknownKey := []byte("unknown-key-cdef0123456789abcdef")

	c, err := New(testKey, testVerify, WithPreviousKeys(oldKey))
	require.NotNil(t, c)
	require.NoError(t, err)

	claims := jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
	}

	// A token signed with the previous key keeps verifying during rotation.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signClaimsV5(t, oldKey, claims))

	got, err := c.checkToken(req)
	require.NoError(t, err)
	require.Equal(t, "test-name", got.Username)

	// A token signed with the current key verifies too.
	req2, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req2.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signClaimsV5(t, testKey, claims))

	_, err = c.checkToken(req2)
	require.NoError(t, err)

	// A token signed with an unlisted key is rejected.
	req3, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req3.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signClaimsV5(t, unknownKey, claims))

	_, err = c.checkToken(req3)
	require.ErrorIs(t, err, ErrInvalidSignature)
}

func TestCheckTokenRejectsMissingExpiration(t *testing.T) {
	t.Parallel()

	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	// Craft a validly-signed token WITHOUT the exp claim: it must be rejected
	// instead of being authorized forever (and must not panic RenewHandler).
	signedToken := signClaimsV5(t, testKey, jwtv5.MapClaims{"username": "test-name"})

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signedToken)

	got, err := c.checkToken(req)
	require.NotNil(t, got)
	require.ErrorIs(t, err, ErrMissingExpiration, "a token without exp must be rejected")

	rr := httptest.NewRecorder()

	require.NotPanics(t, func() { c.RenewHandler(rr, req) })

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	rr2 := httptest.NewRecorder()
	require.False(t, c.IsAuthorized(rr2, req))
}

func TestCheckTokenRejectsUnexpectedAlgorithm(t *testing.T) {
	t.Parallel()

	// The JWT helper is configured with the default HS256 signing method.
	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	// Craft a token signed with a DIFFERENT HMAC algorithm (HS384) using the
	// same key. This must be rejected by the algorithm restriction.
	claims := jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
	}

	signedToken, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS384, claims).SignedString(testKey)
	require.NoError(t, err)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signedToken)

	got, err := c.checkToken(req)
	require.NotNil(t, got)
	require.ErrorIs(t, err, ErrUnexpectedSigningMethod, "a token signed with an unexpected algorithm must be rejected")
}

func TestCheckTokenRejectsAlgNone(t *testing.T) {
	t.Parallel()

	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	// Craft an "alg=none" (unsigned) token: it must be rejected.
	claims := jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
	}

	signedToken, err := jwtv5.NewWithClaims(jwtv5.SigningMethodNone, claims).
		SignedString(jwtv5.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signedToken)

	got, err := c.checkToken(req)
	require.NotNil(t, got)
	require.ErrorIs(t, err, ErrUnexpectedSigningMethod, "an alg=none token must be rejected")
}

func TestCheckTokenRejectsEmptyBearer(t *testing.T) {
	t.Parallel()

	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	// The Authorization header contains the Bearer prefix but an empty token,
	// possibly padded with extra spaces.
	for _, header := range []string{httputil.HeaderAuthBearer, "Bearer    "} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set(DefaultAuthorizationHeader, header)

		got, err := c.checkToken(req)
		require.NotNil(t, got)
		require.ErrorIs(t, err, ErrMissingToken, "an empty bearer token must be rejected")
	}
}

func TestCheckTokenBearerSchemeTolerance(t *testing.T) {
	t.Parallel()

	c, err := New(testKey, testVerify)
	require.NotNil(t, c)
	require.NoError(t, err)

	signedToken := signClaimsV5(t, testKey, jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
	})

	// RFC 7235 §2.1: auth schemes are case-insensitive and the scheme may be
	// separated from the credentials by more than one space.
	for _, prefix := range []string{"Bearer ", "bearer ", "BEARER ", "BeArEr ", "Bearer   "} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set(DefaultAuthorizationHeader, prefix+signedToken)

		_, cerr := c.checkToken(req)
		require.NoError(t, cerr, "prefix %q must be accepted", prefix)
	}
}

func TestCheckTokenClockSkewLeeway(t *testing.T) {
	t.Parallel()

	// A token whose nbf is slightly in the future (as under clock skew).
	signedToken := signClaimsV5(t, testKey, jwtv5.MapClaims{
		"username": "test-name",
		"exp":      time.Now().Add(time.Minute).Unix(),
		"nbf":      time.Now().Add(3 * time.Second).Unix(),
	})

	check := func(c *JWT) error {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signedToken)

		_, cerr := c.checkToken(req)

		return cerr
	}

	// Without leeway the not-yet-valid token is rejected.
	strict, err := New(testKey, testVerify)
	require.NoError(t, err)
	require.ErrorIs(t, check(strict), ErrTokenNotYetValid)

	// With sufficient leeway it is accepted.
	lenient, err := New(testKey, testVerify, WithClockSkewLeeway(10*time.Second))
	require.NoError(t, err)
	require.NoError(t, check(lenient))
}

func TestCheckTokenValidatesIssuerAndAudience(t *testing.T) {
	t.Parallel()

	c, err := New(
		testKey,
		testVerify,
		WithClaimIssuer("iss-1"),
		WithClaimAudience([]string{"aud-1", "aud-2"}),
	)
	require.NotNil(t, c)
	require.NoError(t, err)

	exp := time.Now().Add(time.Minute).Unix()

	tests := []struct {
		name    string
		claims  jwtv5.MapClaims
		wantErr error
	}{
		{
			name:   "valid issuer and audiences",
			claims: jwtv5.MapClaims{"exp": exp, "iss": "iss-1", "aud": []string{"aud-1", "aud-2"}},
		},
		{
			name:    "wrong issuer",
			claims:  jwtv5.MapClaims{"exp": exp, "iss": "other", "aud": []string{"aud-1", "aud-2"}},
			wantErr: ErrInvalidIssuer,
		},
		{
			name:    "missing issuer",
			claims:  jwtv5.MapClaims{"exp": exp, "aud": []string{"aud-1", "aud-2"}},
			wantErr: ErrInvalidIssuer,
		},
		{
			name:    "missing one audience",
			claims:  jwtv5.MapClaims{"exp": exp, "iss": "iss-1", "aud": []string{"aud-1"}},
			wantErr: ErrInvalidAudience,
		},
		{
			name:    "missing audience",
			claims:  jwtv5.MapClaims{"exp": exp, "iss": "iss-1"},
			wantErr: ErrInvalidAudience,
		},
		{
			name:    "single string audience does not satisfy multiple required",
			claims:  jwtv5.MapClaims{"exp": exp, "iss": "iss-1", "aud": "aud-1"},
			wantErr: ErrInvalidAudience,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			req.Header.Set(DefaultAuthorizationHeader, httputil.HeaderAuthBearer+signClaimsV5(t, testKey, tt.claims))

			_, cerr := c.checkToken(req)
			if tt.wantErr != nil {
				require.ErrorIs(t, cerr, tt.wantErr)
			} else {
				require.NoError(t, cerr)
			}
		})
	}
}
