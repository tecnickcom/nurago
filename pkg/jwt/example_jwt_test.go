package jwt_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/jwt"
	"github.com/tecnickcom/nurago/pkg/passwordhash"
)

// Example shows a full login then authorization round-trip. Credentials are
// verified against OWASP-compliant Argon2id hashes via a VerifyCredentialsFn,
// which equalizes timing between known and unknown users with a decoy hash.
func Example() {
	ph := passwordhash.New()

	// Pre-computed password hashes, as they would be stored in a user database.
	aliceHash, err := ph.PasswordHash("s3cr3t-pw")
	if err != nil {
		fmt.Println(err)

		return
	}

	users := map[string]string{"alice": aliceHash}

	// A decoy hash to verify against for unknown users, so response timing does
	// not reveal whether an account exists.
	decoyHash, err := ph.PasswordHash("decoy-pw")
	if err != nil {
		fmt.Println(err)

		return
	}

	verify := func(username, password string) (bool, error) {
		hash, ok := users[username]
		if !ok {
			_, _ = ph.PasswordVerify(password, decoyHash)

			return false, nil
		}

		return ph.PasswordVerify(password, hash)
	}

	auth, err := jwt.New([]byte("0123456789abcdef0123456789abcdef"), verify)
	if err != nil {
		fmt.Println(err)

		return
	}

	ctx := context.Background()

	// Log in with valid credentials and capture the issued token.
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/login", strings.NewReader(`{"username":"alice","password":"s3cr3t-pw"}`))
	auth.LoginHandler(loginRec, loginReq)

	fmt.Println("login status:", loginRec.Code)

	token, _ := io.ReadAll(loginRec.Result().Body)

	// Protect an endpoint with the middleware: the verified claims are available
	// from the request context, identifying the caller.
	protected := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := jwt.ClaimsFromContext(r.Context())
		fmt.Fprintln(w, "hello", claims.Subject)
	}))

	authRec := httptest.NewRecorder()
	authReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/protected", nil)
	authReq.Header.Set(httputil.HeaderAuthorization, httputil.HeaderAuthBearer+string(token))
	protected.ServeHTTP(authRec, authReq)

	fmt.Println("protected status:", authRec.Code)
	fmt.Print(authRec.Body.String())

	// Output:
	// login status: 200
	// protected status: 200
	// hello alice
}

// ExampleNewContextWithClaims validates a raw token received outside an HTTP
// Authorization header, such as gRPC metadata, and stores the claims in the
// context for downstream handlers.
func ExampleNewContextWithClaims() {
	auth, err := jwt.New(
		[]byte("0123456789abcdef0123456789abcdef"),
		func(_, _ string) (bool, error) { return false, nil },
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// The identity is verified by other means; IssueToken only mints the token.
	token, err := auth.IssueToken("alice")
	if err != nil {
		fmt.Println(err)

		return
	}

	claims, err := auth.VerifyToken(token)
	if err != nil {
		fmt.Println(err)

		return
	}

	ctx := jwt.NewContextWithClaims(context.Background(), claims)

	got, ok := jwt.ClaimsFromContext(ctx)

	fmt.Println(ok, got.Subject)

	// Output:
	// true alice
}
