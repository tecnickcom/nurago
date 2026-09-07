package passwordpwned_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/tecnickcom/nurago/pkg/passwordpwned"
)

// rangeServer stands in for https://api.pwnedpasswords.com. It answers the
// k-anonymity range endpoint with hash suffixes and breach counts, which is
// the only thing the client ever sends or receives: the password itself never
// leaves the process.
func rangeServer() *httptest.Server {
	// SHA-1("password") is 5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8, so the
	// client requests prefix 5BAA6 and looks for suffix
	// 1E4C9B93F3F0682250B6CF8331B7EE68FD8 in the response.
	bodies := map[string]string{
		"5BAA6": strings.Join([]string{
			"1E4C9B93F3F0682250B6CF8331B7EE68FD8:10382409",
			"003D68EB55068C33ACE09247EE4C639306B:3",
		}, "\r\n"),
		"ABF7A": "0000000000000000000000000000000000A:0\r\n" +
			"003D68EB55068C33ACE09247EE4C639306B:3",
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := strings.TrimPrefix(r.URL.Path, "/range/")

		body, ok := bodies[prefix]
		if !ok {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write([]byte(body))
	}))
}

func ExampleClient_IsPwnedPassword() {
	srv := rangeServer()
	defer srv.Close()

	// Real code omits WithURL and uses the default HIBP endpoint.
	client, err := passwordpwned.New(passwordpwned.WithURL(srv.URL))
	if err != nil {
		fmt.Println(err)

		return
	}

	pwned, err := client.IsPwnedPassword(context.TODO(), "password")
	fmt.Println("password:", pwned, err)

	pwned, err = client.IsPwnedPassword(context.TODO(), "correct horse battery staple")
	fmt.Println("passphrase:", pwned, err)

	// Output:
	// password: true <nil>
	// passphrase: false <nil>
}

func ExampleClient_PwnedCount() {
	srv := rangeServer()
	defer srv.Close()

	client, err := passwordpwned.New(passwordpwned.WithURL(srv.URL))
	if err != nil {
		fmt.Println(err)

		return
	}

	// PwnedCount returns how many breaches the password appeared in, which
	// supports a threshold policy rather than a flat reject.
	count, err := client.PwnedCount(context.TODO(), "password")

	fmt.Println(count, err)

	// Output:
	// 10382409 <nil>
}
