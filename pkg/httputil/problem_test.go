package httputil

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestNewProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		typeURI    string
		title      string
		detail     string
		want       Problem
	}{
		{
			name:       "empty type defaults to about:blank",
			statusCode: http.StatusNotFound,
			title:      "Missing",
			detail:     "no such user",
			want: Problem{
				Type:   ProblemTypeBlank,
				Title:  "Missing",
				Status: http.StatusNotFound,
				Detail: "no such user",
			},
		},
		{
			name:       "empty title defaults to the status text",
			statusCode: http.StatusConflict,
			typeURI:    "https://example.invalid/probs/conflict",
			detail:     "slot already booked",
			want: Problem{
				Type:   "https://example.invalid/probs/conflict",
				Title:  http.StatusText(http.StatusConflict),
				Status: http.StatusConflict,
				Detail: "slot already booked",
			},
		},
		{
			name:       "both supplied pass through unchanged",
			statusCode: http.StatusForbidden,
			typeURI:    "https://example.invalid/probs/forbidden",
			title:      "Not allowed",
			detail:     "insufficient credit",
			want: Problem{
				Type:   "https://example.invalid/probs/forbidden",
				Title:  "Not allowed",
				Status: http.StatusForbidden,
				Detail: "insufficient credit",
			},
		},
		{
			name:       "both empty with an unknown status code",
			statusCode: 799,
			want: Problem{
				Type:   ProblemTypeBlank,
				Status: 799,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := NewProblem(tt.statusCode, tt.typeURI, tt.title, tt.detail)

			require.Equal(t, tt.want, got)
			require.Empty(t, got.Instance, "Instance is set by the caller")
		})
	}
}

func TestSendProblem(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	p := NewProblem(http.StatusNotFound, "", "", "invalid endpoint")
	p.Instance = "/missing"

	rr := httptest.NewRecorder()
	res.SendProblem(t.Context(), rr, http.StatusNotFound, p)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, MimeApplicationProblemJSON, resp.Header.Get(HeaderContentType)) //nolint:testifylint
	require.Equal(t, "no-cache, no-store, must-revalidate", resp.Header.Get("Cache-Control"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	// Compared byte for byte to pin the member order and the trailing newline.
	want := `{"type":"about:blank","title":"Not Found","status":404,` +
		`"detail":"invalid endpoint","instance":"/missing"}` + "\n"
	require.Equal(t, want, string(body))
}

func TestSendProblem_unknownStatusCodeOmitsTitle(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	res.SendProblem(t.Context(), rr, 499, NewProblem(499, "", "", "client closed request"))

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	want := `{"type":"about:blank","status":499,"detail":"client closed request"}` + "\n"
	require.Equal(t, want, string(body))
}

func TestSendProblem_extensionMembers(t *testing.T) {
	t.Parallel()

	// Extension members are promoted to the top level because Problem has no
	// MarshalJSON method.
	type conflict struct {
		Problem

		ConflictingBookingID string `json:"conflicting_booking_id"`
	}

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	res.SendProblem(t.Context(), rr, http.StatusConflict, conflict{
		Problem:              NewProblem(http.StatusConflict, "", "", "slot already booked"),
		ConflictingBookingID: "b-17",
	})

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var got map[string]any

	require.NoError(t, json.Unmarshal(body, &got))

	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Equal(t, "b-17", got["conflicting_booking_id"])
	require.Equal(t, ProblemTypeBlank, got["type"])
	require.Equal(t, http.StatusText(http.StatusConflict), got["title"])
	require.Len(t, got, 5, "the extension member sits next to the standard members, not nested")
}

func TestSendProblem_marshalError(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	// A channel cannot be marshaled to JSON, so the encode fails before any
	// header is written and the response falls back to a clean 500.
	res.SendProblem(t.Context(), rr, http.StatusBadRequest, make(chan int))

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Equal(t, MimeTextPlain, resp.Header.Get(HeaderContentType))
}

func TestSendProblem_writeError(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	mockWriter := NewMockTestHTTPResponseWriter(gomock.NewController(t))
	mockWriter.EXPECT().Header().AnyTimes().Return(http.Header{})
	mockWriter.EXPECT().WriteHeader(http.StatusBadRequest)
	mockWriter.EXPECT().Write(gomock.Any()).Return(0, errors.New("io error"))

	res.SendProblem(t.Context(), mockWriter, http.StatusBadRequest, NewProblem(http.StatusBadRequest, "", "", ""))
}

func TestSendJSONType(t *testing.T) {
	t.Parallel()

	const contentType = "application/vnd.example.v1+json"

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	res.SendJSONType(t.Context(), rr, http.StatusOK, contentType, map[string]int{"count": 3})

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, contentType, resp.Header.Get(HeaderContentType))
	require.Equal(t, "{\"count\":3}\n", string(body))
}

func TestHTTPResp_problemHandlerFuncs(t *testing.T) {
	t.Parallel()

	const typeURI = "https://example.invalid/probs/generic"

	res := NewHTTPResp(slog.Default())

	tests := []struct {
		name       string
		handler    func() http.HandlerFunc
		statusCode int
		wantDetail string
	}{
		{
			name:       "not found",
			handler:    func() http.HandlerFunc { return res.ProblemNotFoundHandlerFunc(typeURI) },
			statusCode: http.StatusNotFound,
			wantDetail: "invalid endpoint",
		},
		{
			name:       "method not allowed",
			handler:    func() http.HandlerFunc { return res.ProblemMethodNotAllowedHandlerFunc(typeURI) },
			statusCode: http.StatusMethodNotAllowed,
			wantDetail: "the request cannot be routed",
		},
		{
			name:       "panic",
			handler:    func() http.HandlerFunc { return res.ProblemPanicHandlerFunc(typeURI) },
			statusCode: http.StatusInternalServerError,
			wantDetail: "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/some/path?q=1", nil)

			tt.handler().ServeHTTP(rr, req)

			resp := rr.Result()
			require.NotNil(t, resp)

			defer func() {
				require.NoError(t, resp.Body.Close())
			}()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var got Problem

			require.NoError(t, json.Unmarshal(body, &got))

			require.Equal(t, tt.statusCode, resp.StatusCode)
			require.Equal(t, MimeApplicationProblemJSON, resp.Header.Get(HeaderContentType)) //nolint:testifylint
			require.Equal(t, Problem{
				Type:     typeURI,
				Title:    http.StatusText(tt.statusCode),
				Status:   tt.statusCode,
				Detail:   tt.wantDetail,
				Instance: "/some/path",
			}, got)
		})
	}
}

func TestHTTPResp_problemHandlerFuncs_escapedInstance(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	tests := []struct {
		name         string
		target       string
		wantInstance string
	}{
		{
			name:         "encoded separator stays encoded",
			target:       "/orders/a%2Fb",
			wantInstance: "/orders/a%2Fb",
		},
		{
			name:         "space stays percent-encoded",
			target:       "/orders/a%20b",
			wantInstance: "/orders/a%20b",
		},
		{
			name:         "unencoded path is unchanged",
			target:       "/orders/42",
			wantInstance: "/orders/42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, nil)

			res.ProblemNotFoundHandlerFunc("").ServeHTTP(rr, req)

			resp := rr.Result()
			require.NotNil(t, resp)

			defer func() {
				require.NoError(t, resp.Body.Close())
			}()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var got Problem

			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, tt.wantInstance, got.Instance)
		})
	}
}
