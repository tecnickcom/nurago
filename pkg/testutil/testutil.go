/*
Package testutil provides test-only helpers for forcing I/O failures on demand,
capturing process output, bootstrapping HTTP handlers, and normalizing
time-variant values in assertions.

# Helpers

  - Deterministic I/O failure mocks:
    [NewErrorReader] returns a reader whose [ErrorReader.Read] always fails;
    [NewErrorCloser] returns an [io.ReadCloser] whose [ErrorCloser.Close]
    always fails. These exercise error paths that are otherwise difficult to
    trigger.
  - HTTP test bootstrap:
    [RouterWithHandler] builds an [http.Handler] backed by
    `julienschmidt/httprouter` with a route pre-registered.
  - Output capture for assertions:
    [CaptureOutput] redirects stdout, stderr, and the default logger for the
    duration of a function call and returns captured output as a string.
  - Time-variant text normalization:
    [ReplaceDateTime] and [ReplaceUnixTimestamp] replace dynamic timestamp
    fragments in strings (for example JSON responses).

# Usage

	reader := testutil.NewErrorReader("read failed")
	closer := testutil.NewErrorCloser("close failed")
	_ = reader
	_ = closer

	output := testutil.CaptureOutput(t, func() {
	    fmt.Println("hello")
	})

	h := testutil.RouterWithHandler(http.MethodGet, "/health", func(w http.ResponseWriter, _ *http.Request) {
	    w.WriteHeader(http.StatusOK)
	})
	_ = h

	normalized := testutil.ReplaceDateTime(responseBody, "<DATETIME>")
	normalized = testutil.ReplaceUnixTimestamp(normalized, "<UNIX_TS>")
	_ = output
	_ = normalized

These functions are intended for use in tests only.

# When To Use

  - An error branch depends on a failing Read or Close that is hard to
    trigger otherwise.
  - A JSON assertion contains a generated timestamp that must be normalized
    first.
  - A handler needs a real router because it reads path parameters.
*/
package testutil
