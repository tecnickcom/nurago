package profiling_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/julienschmidt/httprouter"
	"github.com/tecnickcom/nurago/pkg/profiling"
)

func ExamplePProfHandler() {
	router := httprouter.New()

	// One wildcard route serves every pprof endpoint. The mount prefix is
	// arbitrary, as the handler reads the endpoint from the wildcard
	// parameter rather than from the request path.
	router.HandlerFunc(
		http.MethodGet,
		"/pprof/*"+profiling.WildcardParamName,
		profiling.PProfHandler,
	)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// The goroutine profile is a named runtime profile forwarded to
	// pprof.Handler, so it needs no explicit registration.
	req, err := http.NewRequestWithContext(
		context.TODO(),
		http.MethodGet,
		srv.URL+"/pprof/goroutine?debug=1",
		nil,
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	fmt.Println(resp.StatusCode)

	// Output:
	// 200
}
