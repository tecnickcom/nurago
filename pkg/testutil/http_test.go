//go:generate mockgen -write_package_comment=false -package httputil -destination ../httputil/testutil_mock_test.go . TestHTTPResponseWriter
//go:generate mockgen -write_package_comment=false -package jsendx -destination ../httputil/jsendx/testutil_mock_test.go . TestHTTPResponseWriter

package testutil

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tecnickcom/nurago/pkg/httputil"
)

func TestRouterWithHandler(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)

	hres := httputil.NewHTTPResp(slog.Default())

	router := RouterWithHandler(http.MethodGet, "/test", func(w http.ResponseWriter, r *http.Request) {
		hres.SendStatus(r.Context(), w, http.StatusOK)
	})
	router.ServeHTTP(rr, req)

	resp := rr.Result()
	require.NotNil(t, resp)

	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err, "error closing resp.Body")
	}()

	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "OK\n", string(body))
}
