package httpserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/random"
	"github.com/tecnickcom/nurago/pkg/redact"
	"github.com/tecnickcom/nurago/pkg/traceid"
)

func TestRequestInjectHandler_debug(t *testing.T) {
	t.Parallel()

	reader, writer, perr := os.Pipe()
	require.NoError(t, perr, "Unexpected error (os.Pipe)")

	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	out := make(chan string)
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		var buf bytes.Buffer

		wg.Done()

		_, err := io.Copy(&buf, reader)
		if err == nil {
			out <- buf.String()
		}
	}()

	wg.Wait()

	nextHandler := http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			// check if the request_time can be retrieved.
			reqTime, ok := httputil.GetRequestTime(r)
			assert.True(t, ok)
			assert.NotEmpty(t, reqTime)
		},
	)
	rnd := random.New(nil)

	handler := RequestInjectHandler(logger, traceid.DefaultHeader, redact.Default().BytesToString, rnd, nextHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(nil, req)

	cerr := writer.Close()
	require.NoError(t, cerr, "Unexpected error (writer.Close)")

	outlog := <-out
	require.NotEmpty(t, outlog, "captured log output")

	require.Contains(t, outlog, "request_dump")
}

func TestRequestInjectHandler_info(t *testing.T) {
	t.Parallel()

	reader, writer, perr := os.Pipe()
	require.NoError(t, perr, "Unexpected error (os.Pipe)")

	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))

	out := make(chan string)
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		var buf bytes.Buffer

		wg.Done()

		_, err := io.Copy(&buf, reader)
		if err == nil {
			out <- buf.String()
		}
	}()

	wg.Wait()

	nextHandler := http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			// check if the request_time can be retrieved.
			reqTime, ok := httputil.GetRequestTime(r)
			assert.True(t, ok)
			assert.NotEmpty(t, reqTime)
			assert.Contains(t, reqTime.String(), "m=", "the request time keeps its monotonic clock reading")
		},
	)

	rnd := random.New(nil)

	handler := RequestInjectHandler(logger, traceid.DefaultHeader, redact.Default().BytesToString, rnd, nextHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(nil, req)

	cerr := writer.Close()
	require.NoError(t, cerr, "Unexpected error (writer.Close)")

	outlog := <-out
	require.NotEmpty(t, outlog, "captured log output")

	require.NotContains(t, outlog, "request_dump")
}

func TestRequestInjectHandler_redactsQuery(t *testing.T) {
	t.Parallel()

	reader, writer, perr := os.Pipe()
	require.NoError(t, perr, "Unexpected error (os.Pipe)")

	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))

	out := make(chan string)
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		var buf bytes.Buffer

		wg.Done()

		_, err := io.Copy(&buf, reader)
		if err == nil {
			out <- buf.String()
		}
	}()

	wg.Wait()

	nextHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	rnd := random.New(nil)

	handler := RequestInjectHandler(logger, traceid.DefaultHeader, redact.Default().BytesToString, rnd, nextHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p?token=SUPERSECRET&x=1", nil)
	handler.ServeHTTP(nil, req)

	cerr := writer.Close()
	require.NoError(t, cerr, "Unexpected error (writer.Close)")

	outlog := <-out
	require.NotEmpty(t, outlog, "captured log output")

	// The secret must not leak through either the query or the request-URI field.
	require.NotContains(t, outlog, "SUPERSECRET", "query secret must be redacted in the inbound log")
	require.Contains(t, outlog, redact.RedactionMarker, "redaction marker must replace the secret")
	require.Contains(t, outlog, "request_query=")
	require.Contains(t, outlog, "request_uri=")
}

// TestRequestInjectHandler_nilRedactFnFailsSafe pins the fallback in
// requestInjectDefaults: a caller that passes no redact function must still get
// redaction (the shared redact.Default()), never an identity function that would
// write query-string secrets to the log verbatim.
func TestRequestInjectHandler_nilRedactFnFailsSafe(t *testing.T) {
	t.Parallel()

	reader, writer, perr := os.Pipe()
	require.NoError(t, perr, "Unexpected error (os.Pipe)")

	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))

	out := make(chan string)
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		buf := new(bytes.Buffer)

		wg.Done()

		_, err := io.Copy(buf, reader)
		if err == nil {
			out <- buf.String()
		}
	}()

	wg.Wait()

	nextHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	// Nil redact function and nil random generator: both must fall back to safe
	// defaults rather than leaking or panicking.
	handler := RequestInjectHandler(logger, traceid.DefaultHeader, nil, nil, nextHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p?token=SUPERSECRET&x=1", nil)
	handler.ServeHTTP(nil, req)

	cerr := writer.Close()
	require.NoError(t, cerr, "Unexpected error (writer.Close)")

	outlog := <-out
	require.NotEmpty(t, outlog, "captured log output")

	require.NotContains(t, outlog, "SUPERSECRET", "an omitted redact function must not leak the query secret")
	require.Contains(t, outlog, redact.RedactionMarker, "the default redactor must replace the secret")
}

// TestRequestInjectHandler_insecureNoRedaction documents the opt-out: naming
// redact.InsecureNoRedaction disables redaction on the real logging path, and the
// query secret is written to the log in the clear. It is the mirror image of
// TestRequestInjectHandler_nilRedactFnFailsSafe: losing redaction takes an
// explicit choice, never an omission.
func TestRequestInjectHandler_insecureNoRedaction(t *testing.T) {
	t.Parallel()

	reader, writer, perr := os.Pipe()
	require.NoError(t, perr, "Unexpected error (os.Pipe)")

	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))

	out := make(chan string)
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		buf := new(bytes.Buffer)

		wg.Done()

		_, err := io.Copy(buf, reader)
		if err == nil {
			out <- buf.String()
		}
	}()

	wg.Wait()

	nextHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	rnd := random.New(nil)

	handler := RequestInjectHandler(logger, traceid.DefaultHeader, redact.InsecureNoRedaction, rnd, nextHandler)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p?token=SUPERSECRET&x=1", nil)
	handler.ServeHTTP(nil, req)

	cerr := writer.Close()
	require.NoError(t, cerr, "Unexpected error (writer.Close)")

	outlog := <-out
	require.NotEmpty(t, outlog, "captured log output")

	require.Contains(t, outlog, "SUPERSECRET", "the bypass must leave the query secret in the clear")
	require.NotContains(t, outlog, redact.RedactionMarker, "nothing must be redacted")
}

func TestRedactRequestURI(t *testing.T) {
	t.Parallel()

	redactFn := redact.Default().BytesToString

	// No query: returned unchanged (the redact function is not invoked).
	require.Equal(t, "/path", redactRequestURI("/path", redactFn))

	// The query portion is redacted while the path is preserved.
	got := redactRequestURI("/path?token=SUPERSECRET&x=1", redactFn)
	require.True(t, strings.HasPrefix(got, "/path?"))
	require.NotContains(t, got, "SUPERSECRET")
	require.Contains(t, got, redact.RedactionMarker)
}
