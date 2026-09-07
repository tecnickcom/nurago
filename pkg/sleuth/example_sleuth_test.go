package sleuth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/tecnickcom/nurago/pkg/sleuth"
)

func ExampleClient_SendDeployRegistration() {
	// A stand-in for https://app.sleuth.io. Real code passes the Sleuth
	// base address.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.Method, r.URL.Path)

		w.WriteHeader(http.StatusOK)
	}))

	defer srv.Close()

	client, err := sleuth.New(srv.URL, "example-org", "api-key")
	if err != nil {
		fmt.Println(err)

		return
	}

	// Requests are validated before being sent, so a malformed payload
	// fails locally rather than at the API.
	err = client.SendDeployRegistration(context.TODO(), &sleuth.DeployRegistrationRequest{
		Deployment:        "payments-production",
		Sha:               "9f8c1b2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b",
		Environment:       "production",
		IgnoreIfDuplicate: true,
		Tags:              []string{"#payments", "#backend"},
	})

	fmt.Println("err:", err)

	// Output:
	// POST /deployments/example-org/payments-production/register_deploy
	// err: <nil>
}

func ExampleClient_SendManualChange() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.Method, r.URL.Path)

		w.WriteHeader(http.StatusOK)
	}))

	defer srv.Close()

	client, err := sleuth.New(
		srv.URL,
		"example-org",
		"api-key",
		sleuth.WithTimeout(5*time.Second),
		sleuth.WithRetryAttempts(3),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// Manual changes record work that did not come from a commit, such as a
	// feature flag flip or a configuration edit.
	err = client.SendManualChange(context.TODO(), &sleuth.ManualChangeRequest{
		Project:     "payments",
		Name:        "enable new pricing",
		Description: "feature flag pricing_v2 turned on",
		Environment: "production",
		Tags:        []string{"#feature-flag"},
	})

	fmt.Println("err:", err)

	// Output:
	// POST /deployments/example-org/payments/register_manual_deploy
	// err: <nil>
}
