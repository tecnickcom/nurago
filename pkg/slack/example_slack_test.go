package slack_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/tecnickcom/nurago/pkg/slack"
)

func ExampleClient_Send() {
	// A stand-in for a Slack Incoming Webhook endpoint. Real code passes the
	// https://hooks.slack.com/services/... address issued by Slack.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Println(string(body))

		_, _ = w.Write([]byte("ok"))
	}))

	defer srv.Close()

	// The constructor takes the defaults applied to every message: sender
	// name, icon emoji, icon URL, and channel.
	client, err := slack.New(srv.URL, "deploybot", ":rocket:", "", "#releases")
	if err != nil {
		fmt.Println(err)

		return
	}

	// Empty metadata arguments fall back to the client defaults.
	err = client.Send(context.TODO(), "release v1.2.3 is live", "", "", "", "")

	fmt.Println("err:", err)

	// Output:
	// {"text":"release v1.2.3 is live","username":"deploybot","icon_emoji":":rocket:","channel":"#releases"}
	// err: <nil>
}

func ExampleClient_Send_override() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Println(string(body))

		_, _ = w.Write([]byte("ok"))
	}))

	defer srv.Close()

	client, err := slack.New(srv.URL, "deploybot", ":rocket:", "", "#releases")
	if err != nil {
		fmt.Println(err)

		return
	}

	// Non-empty arguments override the defaults for this message only.
	err = client.Send(context.TODO(), "build failed", "ci", ":fire:", "", "#alerts")

	fmt.Println("err:", err)

	// Output:
	// {"text":"build failed","username":"ci","icon_emoji":":fire:","channel":"#alerts"}
	// err: <nil>
}
