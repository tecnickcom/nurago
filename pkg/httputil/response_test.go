package httputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/traceid"
	"go.uber.org/mock/gomock"
)

func TestStatus_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  Status
		want    []byte
		wantErr bool
	}{
		{
			name:   "success",
			status: Status(200),
			want:   []byte(`"success"`),
		},
		{
			name:   "error",
			status: Status(500),
			want:   []byte(`"error"`),
		},
		{
			name:   "fail",
			status: Status(400),
			want:   []byte(`"fail"`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.status.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func TestSendStatus(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(nil)

	rr := httptest.NewRecorder()
	res.SendStatus(t.Context(), rr, http.StatusUnauthorized)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, http.StatusText(http.StatusUnauthorized)+"\n", string(body))
}

func TestSendStatus_nonStandardCode(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(nil)

	rr := httptest.NewRecorder()
	res.SendStatus(t.Context(), rr, 499)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, 499, resp.StatusCode)
	require.Equal(t, "Client Error\n", string(body))
}

func TestStatusText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		want       string
	}{
		{name: "known code uses the standard phrase", statusCode: http.StatusTeapot, want: "I'm a teapot"},
		{name: "unknown 1xx", statusCode: 109, want: "Informational"},
		{name: "unknown 2xx", statusCode: 209, want: "Successful"},
		{name: "unknown 3xx", statusCode: 309, want: "Redirection"},
		{name: "unknown 4xx", statusCode: 499, want: "Client Error"},
		{name: "unknown 5xx", statusCode: 529, want: "Server Error"},
		{name: "below the first class", statusCode: 99, want: "Unknown Status"},
		{name: "above the last class", statusCode: 600, want: "Unknown Status"},
		{name: "zero", statusCode: 0, want: "Unknown Status"},
		{name: "negative", statusCode: -1, want: "Unknown Status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, StatusText(tt.statusCode))
		})
	}
}

func TestSendText(t *testing.T) {
	t.Parallel()

	data := "text_data"

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	res.SendText(t.Context(), rr, http.StatusOK, data)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, MimeTextPlain, resp.Header.Get("Content-Type"))
	require.Equal(t, data, string(body))

	// test error condition
	mockWriter := NewMockTestHTTPResponseWriter(gomock.NewController(t))
	mockWriter.EXPECT().Header().AnyTimes().Return(http.Header{})
	mockWriter.EXPECT().WriteHeader(http.StatusOK)
	mockWriter.EXPECT().Write(gomock.Any()).Return(0, errors.New("io error"))
	res.SendText(t.Context(), mockWriter, http.StatusOK, data)
}

func TestSendJSON(t *testing.T) {
	t.Parallel()

	data := "json_data"

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	res.SendJSON(t.Context(), rr, http.StatusOK, data)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, MimeApplicationJSON, resp.Header.Get("Content-Type")) //nolint:testifylint
	require.Equal(t, "\""+data+"\"\n", string(body))

	// test error condition
	mockWriter := NewMockTestHTTPResponseWriter(gomock.NewController(t))
	mockWriter.EXPECT().Header().AnyTimes().Return(http.Header{})
	mockWriter.EXPECT().WriteHeader(http.StatusOK)
	mockWriter.EXPECT().Write(gomock.Any()).Return(0, errors.New("io error"))
	res.SendJSON(t.Context(), mockWriter, http.StatusOK, data)
}

func TestSendXML(t *testing.T) {
	t.Parallel()

	data := "xml_data"

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	res.SendXML(t.Context(), rr, http.StatusOK, XMLHeader, data)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, MimeApplicationXML, resp.Header.Get("Content-Type"))
	require.Equal(t, XMLHeader+"<string>"+data+"</string>", string(body))

	// test error condition: the whole document is buffered and written in a
	// single Write call, so exactly one Write is expected.
	mockWriter := NewMockTestHTTPResponseWriter(gomock.NewController(t))
	mockWriter.EXPECT().Header().AnyTimes().Return(http.Header{})
	mockWriter.EXPECT().WriteHeader(http.StatusOK)
	mockWriter.EXPECT().Write(gomock.Any()).Return(0, errors.New("io error"))
	res.SendXML(t.Context(), mockWriter, http.StatusOK, XMLHeader, data)
}

func TestStatus_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		want        Status
		wantErr     error // sentinel expected via errors.Is
		wantJSONErr bool  // a json decoding error (not ErrInvalidStatus) is expected
	}{
		{name: "success", input: `"success"`, want: Status(http.StatusOK)},
		{name: "fail", input: `"fail"`, want: Status(http.StatusBadRequest)},
		{name: "error", input: `"error"`, want: Status(http.StatusInternalServerError)},
		{name: "invalid status string", input: `"bogus"`, wantErr: ErrInvalidStatus},
		{name: "invalid json type", input: `123`, wantJSONErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got Status

			err := json.Unmarshal([]byte(tt.input), &got)

			switch {
			case tt.wantJSONErr:
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrInvalidStatus)
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			default:
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestHTTPResp_logResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		withTrace  bool
		wantLevel  string
	}{
		{name: "2xx logs at debug", statusCode: http.StatusOK, wantLevel: "DEBUG"},
		{name: "4xx logs at warn", statusCode: http.StatusNotFound, wantLevel: "WARN"},
		{name: "5xx logs at error with trace id", statusCode: http.StatusInternalServerError, withTrace: true, wantLevel: "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			res := NewHTTPResp(logger)

			ctx := t.Context()
			if tt.withTrace {
				ctx = traceid.NewContext(ctx, "trace-abc")
			}

			res.logResponse(ctx, tt.statusCode, logKeyResponseDataText, "payload")

			out := buf.String()
			require.Contains(t, out, `"level":"`+tt.wantLevel+`"`)
			require.Contains(t, out, `"response_code":`)

			if tt.withTrace {
				require.Contains(t, out, `"traceid":"trace-abc"`)
			} else {
				require.NotContains(t, out, "traceid")
			}
		})
	}
}

func TestSendJSON_marshalError(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	// A channel cannot be marshaled to JSON, so the encode fails before any
	// header is written and the response falls back to a clean 500.
	res.SendJSON(t.Context(), rr, http.StatusOK, make(chan int))

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Equal(t, MimeTextPlain, resp.Header.Get("Content-Type"))
}

func TestSendXML_marshalError(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	// A channel cannot be marshaled to XML, so the encode fails before any
	// header is written and the response falls back to a clean 500.
	res.SendXML(t.Context(), rr, http.StatusOK, XMLHeader, make(chan int))

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Equal(t, MimeTextPlain, resp.Header.Get("Content-Type"))
}

func TestSendJSON_clearsStaleContentLength(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	rr := httptest.NewRecorder()
	// A stale Content-Length set by a handler before delegating must be cleared,
	// otherwise net/http rejects the write and truncates the body on a real server.
	rr.Header().Set("Content-Length", "5")

	data := map[string]string{"key": "a value longer than five bytes"}
	res.SendJSON(t.Context(), rr, http.StatusOK, data)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Content-Length"), "stale Content-Length must be cleared")

	var got map[string]string

	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, data, got)
}

func TestSendStatus_writeError(t *testing.T) {
	t.Parallel()

	res := NewHTTPResp(slog.Default())

	mockWriter := NewMockTestHTTPResponseWriter(gomock.NewController(t))
	mockWriter.EXPECT().Header().AnyTimes().Return(http.Header{})
	mockWriter.EXPECT().WriteHeader(http.StatusInternalServerError)
	mockWriter.EXPECT().Write(gomock.Any()).Return(0, errors.New("io error"))
	res.SendStatus(t.Context(), mockWriter, http.StatusInternalServerError)
}
