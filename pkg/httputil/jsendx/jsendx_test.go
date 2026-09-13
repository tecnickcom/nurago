package jsendx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httpserver"
	"github.com/tecnickcom/nurago/pkg/httputil"
	"github.com/tecnickcom/nurago/pkg/testutil"
	"go.uber.org/mock/gomock"
)

func TestSend(t *testing.T) {
	t.Parallel()

	params := &AppInfo{
		ProgramName:    "test",
		ProgramVersion: "1.2.3",
		ProgramRelease: "12345",
	}

	jr := NewJSXResp(nil)

	rr := httptest.NewRecorder()
	jr.Send(t.Context(), rr, http.StatusOK, params, "hello test")

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))

	var okResp Response

	require.NoError(t, json.Unmarshal(body, &okResp), "response must round-trip: %s", body)

	require.Equal(t, "test", okResp.Program, "unexpected response: %s", body)
	require.Equal(t, "1.2.3", okResp.Version, "unexpected response: %s", body)
	require.Equal(t, "12345", okResp.Release, "unexpected response: %s", body)
	require.Equal(t, "OK", okResp.Message, "unexpected response: %s", body)
	require.Equal(t, "hello test", okResp.Data, "unexpected response: %s", body)
	require.Equal(t, httputil.Status(http.StatusOK), okResp.Status, "unexpected response: %s", body)

	// add coverage for error handling
	mockWriter := NewMockTestHTTPResponseWriter(gomock.NewController(t))
	mockWriter.EXPECT().Header().AnyTimes().Return(http.Header{})
	mockWriter.EXPECT().WriteHeader(http.StatusOK)
	mockWriter.EXPECT().Write(gomock.Any()).Return(0, errors.New("io error"))
	jr.Send(t.Context(), mockWriter, http.StatusOK, params, "message")
}

func TestWrapNilInfo(t *testing.T) {
	t.Parallel()

	resp := Wrap(http.StatusOK, nil, "data")

	require.NotNil(t, resp)
	require.Empty(t, resp.Program)
	require.Empty(t, resp.Version)
	require.Empty(t, resp.Release)
	require.Equal(t, http.StatusOK, resp.Code)
	require.Equal(t, "OK", resp.Message)
	require.Equal(t, "data", resp.Data)
}

func TestWrapNonStandardStatusCode(t *testing.T) {
	t.Parallel()

	resp := Wrap(499, nil, "data")

	require.NotNil(t, resp)
	require.Equal(t, 499, resp.Code)
	require.Equal(t, "Client Error", resp.Message)
}

func TestSendNilInfo(t *testing.T) {
	t.Parallel()

	jr := NewJSXResp(nil)

	rr := httptest.NewRecorder()
	jr.Send(t.Context(), rr, http.StatusOK, nil, "hello test")

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	var okResp Response

	require.NoError(t, json.Unmarshal(body, &okResp), "response must round-trip: %s", body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, okResp.Program, "unexpected response: %s", body)
	require.Equal(t, "hello test", okResp.Data, "unexpected response: %s", body)
	require.Equal(t, httputil.Status(http.StatusOK), okResp.Status, "unexpected response: %s", body)
}

func TestDefaultNotFoundHandlerFunc(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "1.1.1",
		ProgramRelease: "1",
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	jr.DefaultNotFoundHandlerFunc(appInfo)(rr, req)

	assertJSendXBody(t, rr, http.StatusNotFound, "{\"program\":\"Test\",\"version\":\"1.1.1\",\"release\":\"1\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"fail\",\"code\":404,\"message\":\"Not Found\",\"data\":\"invalid endpoint\"}\n")
}

func TestDefaultMethodNotAllowedHandlerFunc(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "2.2.2",
		ProgramRelease: "2",
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	jr.DefaultMethodNotAllowedHandlerFunc(appInfo)(rr, req)

	assertJSendXBody(t, rr, http.StatusMethodNotAllowed, "{\"program\":\"Test\",\"version\":\"2.2.2\",\"release\":\"2\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"fail\",\"code\":405,\"message\":\"Method Not Allowed\",\"data\":\"the request cannot be routed\"}\n")
}

func TestDefaultPanicHandlerFunc(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "3.3.3",
		ProgramRelease: "3",
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	jr.DefaultPanicHandlerFunc(appInfo)(rr, req)

	assertJSendXBody(t, rr, http.StatusInternalServerError, "{\"program\":\"Test\",\"version\":\"3.3.3\",\"release\":\"3\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"error\",\"code\":500,\"message\":\"Internal Server Error\",\"data\":\"internal error\"}\n")
}

func TestDefaultIndexHandler(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "4.4.4",
		ProgramRelease: "4",
	}

	routes := []httpserver.Route{
		{
			Method:      http.MethodGet,
			Path:        "/get",
			Handler:     nil,
			Description: "Get endpoint",
		},
		{
			Method:      http.MethodPost,
			Path:        "/post",
			Handler:     nil,
			Description: "Post endpoint",
		},
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	jr.DefaultIndexHandler(appInfo)(routes).ServeHTTP(rr, req)

	assertJSendXBody(t, rr, http.StatusOK, "{\"program\":\"Test\",\"version\":\"4.4.4\",\"release\":\"4\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"success\",\"code\":200,\"message\":\"OK\",\"data\":{\"routes\":[{\"method\":\"GET\",\"path\":\"/get\",\"description\":\"Get endpoint\"},{\"method\":\"POST\",\"path\":\"/post\",\"description\":\"Post endpoint\"}]}}\n")
}

func TestDefaultIPHandler(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "5.5.5",
		ProgramRelease: "5",
	}

	tests := []struct {
		name    string
		ipFunc  httpserver.GetPublicIPFunc
		wantIP  string
		wantErr bool
	}{
		{
			name:    "success response",
			ipFunc:  func(_ context.Context) (string, error) { return "0.0.0.0", nil },
			wantIP:  "0.0.0.0",
			wantErr: false,
		},
		{
			name:    "error response",
			ipFunc:  func(_ context.Context) (string, error) { return "ERROR", errors.New("ERROR") },
			wantIP:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

			rr := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			jr.DefaultIPHandler(appInfo, tt.ipFunc).ServeHTTP(rr, req)

			if tt.wantErr {
				assertJSendXBody(t, rr, http.StatusFailedDependency, "{\"program\":\"Test\",\"version\":\"5.5.5\",\"release\":\"5\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"fail\",\"code\":424,\"message\":\"Failed Dependency\",\"data\":\"ERROR\"}\n")
			} else {
				assertJSendXBody(t, rr, http.StatusOK, "{\"program\":\"Test\",\"version\":\"5.5.5\",\"release\":\"5\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"success\",\"code\":200,\"message\":\"OK\",\"data\":\"0.0.0.0\"}\n")
			}
		})
	}
}

func TestDefaultPingHandler(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "6.6.6",
		ProgramRelease: "6",
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	jr.DefaultPingHandler(appInfo)(rr, req)

	assertJSendXBody(t, rr, http.StatusOK, "{\"program\":\"Test\",\"version\":\"6.6.6\",\"release\":\"6\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"success\",\"code\":200,\"message\":\"OK\",\"data\":\"OK\"}\n")
}

func TestDefaultStatusHandler(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "7.7.7",
		ProgramRelease: "7",
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	jr.DefaultStatusHandler(appInfo)(rr, req)

	assertJSendXBody(t, rr, http.StatusOK, "{\"program\":\"Test\",\"version\":\"7.7.7\",\"release\":\"7\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"success\",\"code\":200,\"message\":\"OK\",\"data\":\"OK\"}\n")
}

func TestHealthCheckResultWriter(t *testing.T) {
	t.Parallel()

	appInfo := &AppInfo{
		ProgramName:    "Test",
		ProgramVersion: "8.8.8",
		ProgramRelease: "8",
	}

	jr := NewJSXResp(httputil.NewHTTPResp(slog.Default()))

	rr := httptest.NewRecorder()
	jr.HealthCheckResultWriter(appInfo)(t.Context(), rr, http.StatusOK, "test body")

	assertJSendXBody(t, rr, http.StatusOK, "{\"program\":\"Test\",\"version\":\"8.8.8\",\"release\":\"8\",\"datetime\":\"1970-01-01T00:00:00\",\"timestamp\":0,\"status\":\"success\",\"code\":200,\"message\":\"OK\",\"data\":\"test body\"}\n")
}

// assertJSendXBody asserts that the recorded response has the expected status
// code, the JSendX content type, and (after normalizing the variable datetime
// and timestamp fields) a body matching wantJSON.
func assertJSendXBody(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, wantJSON string) {
	t.Helper()

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		require.NoError(t, resp.Body.Close(), "error closing resp.Body")
	}()

	bodyData, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	body := string(bodyData)
	body = testutil.ReplaceDateTime(body, "1970-01-01T00:00:00")
	body = testutil.ReplaceUnixTimestamp(body, "0")

	require.Equal(t, wantStatus, resp.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	require.JSONEq(t, wantJSON, body)
}
