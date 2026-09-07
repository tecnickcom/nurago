package devlake_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/tecnickcom/nurago/pkg/devlake"
)

func ExampleClient_SendDeployment() {
	// A stand-in for a DevLake instance. Real code passes the base address
	// of the deployment.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.Method, r.URL.Path)

		w.WriteHeader(http.StatusOK)
	}))

	defer srv.Close()

	client, err := devlake.New(srv.URL, "api-key")
	if err != nil {
		fmt.Println(err)

		return
	}

	started := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 9, 7, 10, 4, 0, 0, time.UTC)

	// Requests are validated before being sent, so a malformed payload
	// fails locally rather than at the API.
	err = client.SendDeployment(context.TODO(), &devlake.DeploymentRequest{
		ConnectionID: 1,
		ID:           "deploy-2026-09-07-001",
		DisplayTitle: "payments v1.2.3",
		Result:       "SUCCESS",
		Environment:  "PRODUCTION",
		StartedDate:  &started,
		FinishedDate: &finished,
	})

	fmt.Println("err:", err)

	// Output:
	// POST /api/rest/plugins/webhook/connections/1/deployments
	// err: <nil>
}

func ExampleClient_SendDeployment_validation() {
	client, err := devlake.New("https://devlake.example.com", "api-key")
	if err != nil {
		fmt.Println(err)

		return
	}

	// ID and the two dates are required, so this request never reaches the
	// network.
	err = client.SendDeployment(context.TODO(), &devlake.DeploymentRequest{
		ConnectionID: 1,
	})

	fmt.Println(err != nil)

	// Output:
	// true
}

func ExampleClient_HealthCheck() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	defer srv.Close()

	client, err := devlake.New(
		srv.URL,
		"api-key",
		devlake.WithPingURL(srv.URL),
		devlake.WithPingTimeout(2*time.Second),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// HealthCheck satisfies healthcheck.HealthChecker, so the dependency can
	// be reported on a service health endpoint.
	fmt.Println(client.HealthCheck(context.TODO()))

	// Output:
	// <nil>
}
