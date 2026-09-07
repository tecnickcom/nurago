package ipify_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/tecnickcom/nurago/pkg/ipify"
)

func ExampleClient_GetPublicIP() {
	// A stand-in for https://api.ipify.org, which returns the caller
	// address as plain text. Real code omits WithURL and uses the default.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("192.0.2.1"))
	}))

	defer srv.Close()

	client, err := ipify.New(
		ipify.WithURL(srv.URL),
		ipify.WithTimeout(2*time.Second),
		ipify.WithErrorIP("0.0.0.0"), // returned instead of an empty string on failure
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	ip, err := client.GetPublicIP(context.TODO())

	fmt.Println(ip, err)

	// Output:
	// 192.0.2.1 <nil>
}

func ExampleClient_HealthCheck() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("192.0.2.1"))
	}))

	defer srv.Close()

	client, err := ipify.New(ipify.WithURL(srv.URL))
	if err != nil {
		fmt.Println(err)

		return
	}

	// HealthCheck fits the healthcheck package's HealthChecker contract, so
	// the dependency can be reported on a service /healthz endpoint.
	fmt.Println(client.HealthCheck(context.TODO()))

	// Output:
	// <nil>
}
