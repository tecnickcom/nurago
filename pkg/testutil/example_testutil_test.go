package testutil_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/tecnickcom/nurago/pkg/testutil"
)

func ExampleReplaceDateTime() {
	// A JSON response carrying a generated timestamp cannot be compared
	// against a fixed golden value until the timestamp is normalized.
	body := `{"id":"7","created_at":"2026-09-07T15:04:05.123Z","status":"ok"}`

	fmt.Println(testutil.ReplaceDateTime(body, "<TIME>"))

	// Output:
	// {"id":"7","created_at":"<TIME>","status":"ok"}
}

func ExampleReplaceUnixTimestamp() {
	body := `{"event":"login","ts":1757254800123456789}`

	// Standalone 19-digit integers are treated as Unix-nanosecond
	// timestamps. Shorter or longer numbers are left alone.
	fmt.Println(testutil.ReplaceUnixTimestamp(body, "<TS>"))
	fmt.Println(testutil.ReplaceUnixTimestamp(`{"count":42}`, "<TS>"))

	// Output:
	// {"event":"login","ts":<TS>}
	// {"count":42}
}

func ExampleNewErrorReader() {
	// ErrorReader fails on every Read, which exercises the error branch of
	// code that consumes an io.Reader.
	r := testutil.NewErrorReader("simulated read failure")

	_, err := io.ReadAll(r)

	fmt.Println(err)

	// Output:
	// simulated read failure
}

func ExampleNewErrorCloser() {
	// ErrorCloser fails on Close, covering the deferred-close error path
	// that is otherwise difficult to trigger.
	c := testutil.NewErrorCloser("simulated close failure")

	fmt.Println(c.Close())

	// Output:
	// simulated close failure
}

func ExampleRouterWithHandler() {
	// The handler under test reads an httprouter path parameter, so it
	// needs a real router rather than a bare http.HandlerFunc.
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		err := json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
		if err != nil {
			fmt.Println(err)
		}
	}

	router := testutil.RouterWithHandler(http.MethodGet, "/users/:id", handler)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(
		context.TODO(), http.MethodGet, "/users/42", nil,
	))

	fmt.Println(rec.Code)
	fmt.Print(rec.Body.String())

	// Output:
	// 200
	// {"path":"/users/42"}
}
