package jirasrv_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/tecnickcom/nurago/pkg/jirasrv"
)

func ExampleClient_SendRequest() {
	// A stand-in for a Jira Server instance. Real code passes the Jira base
	// address and a personal access token.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.Method, r.URL.Path, r.URL.RawQuery)
		fmt.Println("auth:", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")

		_, _ = w.Write([]byte(`{"key":"PROJ-42","fields":{"summary":"login fails"}}`))
	}))

	defer srv.Close()

	client, err := jirasrv.New(srv.URL, "personal-access-token")
	if err != nil {
		fmt.Println(err)

		return
	}

	query := url.Values{}
	query.Set("fields", "summary")

	// SendRequest covers any Jira REST endpoint, so the package does not
	// have to model the whole API surface.
	resp, err := client.SendRequest(
		context.TODO(),
		http.MethodGet,
		"/issue/PROJ-42",
		&query,
		nil,
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// The base path (/rest/api/2) is applied by the client, so endpoints are
	// given relative to it. The caller owns the response body.
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	var issue struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}

	err = json.Unmarshal(body, &issue)

	fmt.Println(issue.Key, issue.Fields.Summary, err)

	// Output:
	// GET /rest/api/2/issue/PROJ-42 fields=summary
	// auth: Bearer personal-access-token
	// PROJ-42 login fails <nil>
}

func ExampleClient_SendRequest_update() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		fmt.Println(r.Method, r.URL.Path)
		fmt.Print(string(body))

		w.WriteHeader(http.StatusNoContent)
	}))

	defer srv.Close()

	client, err := jirasrv.New(srv.URL, "personal-access-token")
	if err != nil {
		fmt.Println(err)

		return
	}

	// IssueUpdate models the Jira edit payload, so field operations do not
	// have to be assembled as raw maps.
	update := &jirasrv.IssueUpdate{
		Fields: map[string]any{"summary": "login fails on mobile"},
	}

	resp, err := client.SendRequest(
		context.TODO(),
		http.MethodPut,
		"/issue/PROJ-42",
		nil,
		update,
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	fmt.Println(resp.StatusCode)

	// Output:
	// PUT /rest/api/2/issue/PROJ-42
	// {"fields":{"summary":"login fails on mobile"}}
	// 204
}
