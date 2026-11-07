package srvlifecycle_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/tecnickcom/nurago/pkg/srvlifecycle"
)

func ExampleNew() {
	ctx := context.Background()

	var lc net.ListenConfig

	ls, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)
		return
	}

	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "OK")
		}),
	}

	// The wait group and signal channel are shared by every server of the
	// service, for example through pkg/bootstrap.
	wg := &sync.WaitGroup{}
	signal := make(chan struct{})

	// *http.Server satisfies srvlifecycle.Server.
	life, err := srvlifecycle.New(srv, ls,
		srvlifecycle.WithName("http"),
		srvlifecycle.WithLogger(slog.New(slog.DiscardHandler)),
		srvlifecycle.WithShutdownTimeout(5*time.Second),
		srvlifecycle.WithShutdownWaitGroup(wg),
		srvlifecycle.WithShutdownSignalChan(signal),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	life.Start(ctx)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+life.Addr().String()+"/", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	fmt.Println(resp.StatusCode, string(body))

	// Closing the signal channel shuts down every server that shares it.
	close(signal)
	wg.Wait()

	fmt.Println("server stopped")

	// Output:
	// 200 OK
	// server stopped
}
