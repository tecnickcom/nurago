package httputil

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/tecnickcom/nurago/pkg/traceid"
)

const (
	// MimeApplicationJSON contains the mime type string for JSON content.
	MimeApplicationJSON = "application/json; charset=utf-8"

	// MimeApplicationXML contains the mime type string for XML content.
	MimeApplicationXML = "application/xml; charset=utf-8"

	// MimeApplicationProblemJSON contains the mime type string for RFC 9457 problem
	// details content. Unlike the other mime constants it carries no charset
	// parameter: RFC 9457 registers the media type without one.
	MimeApplicationProblemJSON = "application/problem+json"

	// MimeTextPlain contains the mime type string for text content.
	MimeTextPlain = "text/plain; charset=utf-8"
)

// XMLHeader is a default XML Declaration header suitable for use with the SendXML function.
const XMLHeader = xml.Header

// JSend status codes.
const (
	StatusSuccess = "success"
	StatusFail    = "fail"
	StatusError   = "error"
)

// log keys for response logging.
const (
	logKeyResponseDataText   = "response_txt"
	logKeyResponseDataObject = "response_data"
)

// ErrInvalidStatus is returned when unmarshaling a JSend status value that is
// not one of "success", "fail", or "error".
var ErrInvalidStatus = errors.New("invalid JSend status")

// Status translates the HTTP status code to a JSend status string.
//
// The value-receiver String/MarshalJSON and pointer-receiver UnmarshalJSON mix is
// required: UnmarshalJSON must mutate the receiver, while String/MarshalJSON must
// work on non-addressable values.
type Status int

// String projects the HTTP status code onto a JSend status string:
//   - codes below 400 (including 0 and negative values) map to "success"
//   - codes in the 400-499 range map to "fail"
//   - codes 500 and above map to "error"
func (sc Status) String() string {
	s := StatusSuccess

	if sc >= http.StatusBadRequest { // 400+
		s = StatusFail
	}

	if sc >= http.StatusInternalServerError { // 500+
		s = StatusError
	}

	return s
}

// MarshalJSON implements the custom marshaling function for the json encoder,
// emitting the JSend status string (see [Status.String]). Because Status also
// implements [fmt.Stringer], slog text handlers render the same string.
func (sc Status) MarshalJSON() ([]byte, error) {
	return json.Marshal(sc.String()) //nolint:wrapcheck
}

// UnmarshalJSON implements the custom unmarshaling function for the json decoder.
//
// A JSend status string is mapped back to a representative HTTP status code. The
// mapping is intentionally lossy: the original code is not recoverable from the
// status string alone and should be read from the accompanying code field (e.g.
// jsendx.Response.Code):
//   - "success" maps to 200 (http.StatusOK)
//   - "fail"    maps to 400 (http.StatusBadRequest)
//   - "error"   maps to 500 (http.StatusInternalServerError)
//
// Any other value yields [ErrInvalidStatus].
func (sc *Status) UnmarshalJSON(data []byte) error {
	var s string

	err := json.Unmarshal(data, &s)
	if err != nil {
		return err //nolint:wrapcheck
	}

	switch s {
	case StatusSuccess:
		*sc = http.StatusOK
	case StatusFail:
		*sc = http.StatusBadRequest
	case StatusError:
		*sc = http.StatusInternalServerError
	default:
		return fmt.Errorf("%w: %q", ErrInvalidStatus, s)
	}

	return nil
}

// statusCodeLimit is one past the highest assignable HTTP status code.
const statusCodeLimit = 600

// StatusText returns the standard reason phrase for statusCode, falling back to the
// name of its RFC 9110 status class when net/http knows no phrase for the code.
//
// [http.StatusText] returns an empty string for non-standard codes such as 499 or 529,
// which would leave a response body, log entry or message field with no description of
// the status at all. Codes outside the 100-599 range yield "Unknown Status".
func StatusText(statusCode int) string {
	if s := http.StatusText(statusCode); s != "" {
		return s
	}

	switch {
	case statusCode < http.StatusContinue, statusCode >= statusCodeLimit:
		return "Unknown Status"
	case statusCode < http.StatusOK: // 1xx
		return "Informational"
	case statusCode < http.StatusMultipleChoices: // 2xx
		return "Successful"
	case statusCode < http.StatusBadRequest: // 3xx
		return "Redirection"
	case statusCode < http.StatusInternalServerError: // 4xx
		return "Client Error"
	default: // 5xx
		return "Server Error"
	}
}

// HTTPResp holds the configuration for the HTTP response methods.
type HTTPResp struct {
	logger *slog.Logger
}

// NewHTTPResp constructs HTTP response helper with structured logging to provided logger (or slog.Default() if nil).
func NewHTTPResp(l *slog.Logger) *HTTPResp {
	if l == nil {
		l = slog.Default()
	}

	return &HTTPResp{
		logger: l,
	}
}

// SendStatus writes HTTP status code with standard text and logs response entry.
//
// The body is the reason phrase from [StatusText], so a non-standard status code
// still produces a description instead of an empty line.
func (hr *HTTPResp) SendStatus(ctx context.Context, w http.ResponseWriter, statusCode int) {
	defer hr.logResponse(ctx, statusCode, logKeyResponseDataText, "")

	writeHeaders(w, statusCode, MimeTextPlain)

	_, err := w.Write([]byte(StatusText(statusCode) + "\n"))
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, "httputil.SendStatus()")
	}
}

// SendText writes plain text response with cache-control headers and structured logging.
func (hr *HTTPResp) SendText(ctx context.Context, w http.ResponseWriter, statusCode int, data string) {
	defer hr.logResponse(ctx, statusCode, logKeyResponseDataText, data)

	writeHeaders(w, statusCode, MimeTextPlain)

	_, err := w.Write([]byte(data)) //nolint:gosec
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, "httputil.SendText()")
	}
}

// SendJSON encodes data as JSON, writes with cache-control headers, and logs response entry.
//
// The payload is marshaled fully before any header or status code is written, so a
// marshaling failure produces a clean 500 Internal Server Error instead of a partial
// or empty body sent under the requested (success) status code.
func (hr *HTTPResp) SendJSON(ctx context.Context, w http.ResponseWriter, statusCode int, data any) {
	hr.writeJSON(ctx, w, statusCode, MimeApplicationJSON, "httputil.SendJSON()", data)
}

// SendJSONType encodes data as JSON with a caller-supplied content type, writes it with
// cache-control headers, and logs the response entry.
//
// It serves JSON-based media types that have no dedicated method, such as
// application/ld+json or any application/vnd.*+json variant. Use [HTTPResp.SendJSON]
// for application/json and [HTTPResp.SendProblem] for application/problem+json.
//
// The payload is marshaled fully before any header or status code is written, so a
// marshaling failure produces a clean 500 Internal Server Error instead of a partial
// or empty body sent under the requested (success) status code.
func (hr *HTTPResp) SendJSONType(
	ctx context.Context,
	w http.ResponseWriter,
	statusCode int,
	contentType string,
	data any,
) {
	hr.writeJSON(ctx, w, statusCode, contentType, "httputil.SendJSONType()", data)
}

// SendXML encodes data as XML with header prefix, cache-control headers, and structured logging.
//
// The document (declaration header plus encoded payload) is buffered fully before any
// header or status code is written, so an encoding failure produces a clean 500
// Internal Server Error instead of a truncated document under a success status code.
func (hr *HTTPResp) SendXML(ctx context.Context, w http.ResponseWriter, statusCode int, xmlHeader string, data any) {
	var buf bytes.Buffer

	buf.WriteString(xmlHeader)

	err := xml.NewEncoder(&buf).Encode(data)
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, "httputil.SendXML()")
		hr.SendStatus(ctx, w, http.StatusInternalServerError)

		return
	}

	defer hr.logResponse(ctx, statusCode, logKeyResponseDataObject, data)

	writeHeaders(w, statusCode, MimeApplicationXML)

	_, err = w.Write(buf.Bytes())
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, "httputil.SendXML()")
	}
}

// writeJSON encodes data as JSON and writes it under contentType with cache-control
// headers, logging the response entry. It backs [HTTPResp.SendJSON],
// [HTTPResp.SendJSONType] and [HTTPResp.SendProblem].
//
// The payload is marshaled fully before any header or status code is written, so a
// marshaling failure produces a clean 500 Internal Server Error instead of a partial
// or empty body sent under the requested (success) status code.
//
// logName names the calling method in error log entries.
func (hr *HTTPResp) writeJSON(
	ctx context.Context,
	w http.ResponseWriter,
	statusCode int,
	contentType string,
	logName string,
	data any,
) {
	body, err := json.Marshal(data)
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, logName)
		hr.SendStatus(ctx, w, http.StatusInternalServerError)

		return
	}

	defer hr.logResponse(ctx, statusCode, logKeyResponseDataObject, data)

	writeHeaders(w, statusCode, contentType)

	// Append the trailing newline to keep byte-compatibility with json.Encoder.Encode.
	_, err = w.Write(append(body, '\n'))
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, logName)
	}
}

// writeHeaders sets the content type and disables caching and MIME sniffing.
func writeHeaders(w http.ResponseWriter, statusCode int, contentType string) {
	h := w.Header()
	// Clear any Content-Length a caller set before delegating: the body written
	// here rarely matches it, and a stale value makes net/http reject the write
	// and truncate the response (parity with http.Error).
	h.Del("Content-Length")
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set(HeaderContentType, contentType)
	w.WriteHeader(statusCode)
}

// logResponse logs the response.
//
// The response payload (under dataKey) is logged in full; for 4xx/5xx responses it
// is emitted at Warn/Error level regardless of the debug setting, so be mindful of
// payload volume and any sensitive data it may contain.
//
// Level mapping: 5xx logs at Error, 4xx at Warn, and everything else at Debug.
func (hr *HTTPResp) logResponse(ctx context.Context, statusCode int, dataKey string, data any) {
	level := slog.LevelDebug

	switch {
	case statusCode >= http.StatusInternalServerError: // 500+
		level = slog.LevelError
	case statusCode >= http.StatusBadRequest: // 400-499
		level = slog.LevelWarn
	}

	// Skip building attributes (timestamps, payload, trace-ID lookup) when the
	// record would be discarded anyway; this is the common 2xx-at-Info case.
	if !hr.logger.Enabled(ctx, level) {
		return
	}

	resTime := time.Now().UTC()

	reqTime, ok := GetRequestTimeFromContext(ctx)
	if !ok {
		reqTime = resTime
	}

	attrs := []slog.Attr{
		slog.Int("response_code", statusCode),
		slog.String("response_message", StatusText(statusCode)),
		slog.Any("response_status", Status(statusCode)),
		slog.Time("response_time", resTime),
		slog.Duration("response_duration", resTime.Sub(reqTime)),
		slog.Any(dataKey, data),
	}

	// Correlate the response entry with the request entry when a trace ID is present.
	if id := traceid.FromContext(ctx, ""); id != "" {
		attrs = append(attrs, slog.String(traceid.DefaultLogKey, id))
	}

	hr.logger.LogAttrs(ctx, level, "Response", attrs...)
}
