package httputil

import (
	"context"
	"net/http"
)

// ProblemTypeBlank is the problem type URI used when no type is given.
const ProblemTypeBlank = "about:blank"

// Problem is an RFC 9457 Problem Details object.
//
// Extension members are added by embedding Problem in an outer struct. Problem has
// no MarshalJSON method so the promoted fields stay inline in the encoded object;
// adding one would shadow the outer struct's own fields.
type Problem struct {
	// Type is a URI reference identifying the problem type.
	Type string `json:"type"`

	// Title is a short human-readable summary of the problem type, constant for a given Type.
	// It is omitted when empty, as RFC 9457 makes it optional and an empty summary carries
	// no more information than an absent one.
	Title string `json:"title,omitempty"`

	// Status is the HTTP status code.
	Status int `json:"status,omitempty"`

	// Detail is a human-readable explanation specific to this occurrence.
	Detail string `json:"detail,omitempty"`

	// Instance is a URI reference identifying this specific occurrence.
	Instance string `json:"instance,omitempty"`
}

// NewProblem returns a Problem for statusCode.
//
// An empty typeURI is replaced with [ProblemTypeBlank] and an empty title with the
// standard status text for statusCode, which is what RFC 9457 prescribes for the
// about:blank type. Title stays empty, and is then omitted from the encoded document,
// for status codes net/http has no text for. Instance is left empty for the caller to
// set, as it identifies a single occurrence and usually derives from the request.
func NewProblem(statusCode int, typeURI, title, detail string) Problem {
	if typeURI == "" {
		typeURI = ProblemTypeBlank
	}

	if title == "" {
		title = http.StatusText(statusCode)
	}

	return Problem{
		Type:   typeURI,
		Title:  title,
		Status: statusCode,
		Detail: detail,
	}
}

// SendProblem encodes data as an RFC 9457 problem details document, writes it with
// cache-control headers, and logs the response entry.
//
// The data payload is typically a [Problem] or a struct embedding one. It is marshaled
// fully before any header or status code is written, so a marshaling failure produces
// a clean 500 Internal Server Error instead of a partial body.
func (hr *HTTPResp) SendProblem(ctx context.Context, w http.ResponseWriter, statusCode int, data any) {
	hr.writeJSON(ctx, w, statusCode, MimeApplicationProblemJSON, "httputil.SendProblem()", data)
}

// sendRequestProblem sends a Problem for statusCode with Instance set to the request path.
//
// The path is taken in its escaped form so Instance is a valid URI reference and
// still denotes the requested resource: the decoded URL.Path collapses an encoded
// %2F into a path separator and leaves characters such as a space unescaped.
//
// Reflecting the path is safe here: it is JSON-encoded, the media type is not
// text/html, and writeHeaders sets X-Content-Type-Options: nosniff.
func (hr *HTTPResp) sendRequestProblem(
	w http.ResponseWriter,
	r *http.Request,
	statusCode int,
	typeURI, detail string,
) {
	p := NewProblem(statusCode, typeURI, "", detail)
	p.Instance = r.URL.EscapedPath()

	hr.SendProblem(r.Context(), w, statusCode, p)
}

// ProblemNotFoundHandlerFunc returns the handler used when no route matches,
// answering in RFC 9457 format.
func (hr *HTTPResp) ProblemNotFoundHandlerFunc(typeURI string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hr.sendRequestProblem(w, r, http.StatusNotFound, typeURI, "invalid endpoint")
	}
}

// ProblemMethodNotAllowedHandlerFunc returns the handler used when a route exists but
// the method is not allowed, answering in RFC 9457 format.
func (hr *HTTPResp) ProblemMethodNotAllowedHandlerFunc(typeURI string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hr.sendRequestProblem(w, r, http.StatusMethodNotAllowed, typeURI, "the request cannot be routed")
	}
}

// ProblemPanicHandlerFunc returns the handler for recovered panics, answering in
// RFC 9457 format.
func (hr *HTTPResp) ProblemPanicHandlerFunc(typeURI string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hr.sendRequestProblem(w, r, http.StatusInternalServerError, typeURI, "internal error")
	}
}
