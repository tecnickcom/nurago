package httpretrier_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/tecnickcom/nurago/pkg/httpretrier"
)

func ExampleHTTPRetrier_Do() {
	// A server that fails twice before succeeding.
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++

		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	defer srv.Close()

	retrier, err := httpretrier.New(
		srv.Client(),
		httpretrier.WithAttempts(4),
		httpretrier.WithDelay(time.Millisecond),
		httpretrier.WithJitter(time.Microsecond), // kept tiny so the example runs quickly
		httpretrier.WithRetryIfFn(httpretrier.RetryIfForReadRequests),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	req, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, srv.URL, nil)
	if err != nil {
		fmt.Println(err)

		return
	}

	resp, err := retrier.Do(req)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	fmt.Println(resp.StatusCode, "after", attempts, "attempts")

	// Output:
	// 200 after 3 attempts
}

func ExampleRetryIfFnByHTTPMethod() {
	// Read requests are safe to replay, so 5xx responses are retried.
	// Write requests use a stricter policy, since replaying them may
	// duplicate a side effect.
	readPolicy := httpretrier.RetryIfFnByHTTPMethod(http.MethodGet)
	writePolicy := httpretrier.RetryIfFnByHTTPMethod(http.MethodPost)

	resp := &http.Response{StatusCode: http.StatusNotFound}

	fmt.Println(readPolicy(resp, nil), writePolicy(resp, nil))

	// Output:
	// true false
}

func ExampleWithOnRetry() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	defer srv.Close()

	// OnRetry observes every retry decision, which is the hook to use for
	// logging or for incrementing a metrics counter.
	retrier, err := httpretrier.New(
		srv.Client(),
		httpretrier.WithAttempts(3),
		httpretrier.WithDelay(time.Millisecond),
		httpretrier.WithJitter(time.Microsecond),
		httpretrier.WithRetryIfFn(httpretrier.RetryIfForReadRequests),
		httpretrier.WithOnRetry(func(attempt uint, _ time.Duration, r *http.Response, _ error) {
			fmt.Println("retry", attempt, "after status", r.StatusCode)
		}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	req, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, srv.URL, nil)
	if err != nil {
		fmt.Println(err)

		return
	}

	resp, err := retrier.Do(req)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	fmt.Println("final:", resp.StatusCode)

	// Output:
	// retry 1 after status 429
	// retry 2 after status 429
	// final: 429
}
