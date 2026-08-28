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
func (hr *HTTPResp) SendStatus(ctx context.Context, w http.ResponseWriter, statusCode int) {
	defer hr.logResponse(ctx, statusCode, logKeyResponseDataText, "")

	writeHeaders(w, statusCode, MimeTextPlain)

	_, err := w.Write([]byte(http.StatusText(statusCode) + "\n"))
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
	body, err := json.Marshal(data)
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, "httputil.SendJSON()")
		hr.SendStatus(ctx, w, http.StatusInternalServerError)

		return
	}

	defer hr.logResponse(ctx, statusCode, logKeyResponseDataObject, data)

	writeHeaders(w, statusCode, MimeApplicationJSON)

	// Append the trailing newline to keep byte-compatibility with json.Encoder.Encode.
	_, err = w.Write(append(body, '\n'))
	if err != nil {
		hr.logger.With(slog.Any("error", err)).ErrorContext(ctx, "httputil.SendJSON()")
	}
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
		slog.String("response_message", http.StatusText(statusCode)),
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
