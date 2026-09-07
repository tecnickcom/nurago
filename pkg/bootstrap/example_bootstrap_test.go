package bootstrap_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tecnickcom/nurago/pkg/bootstrap"
	"github.com/tecnickcom/nurago/pkg/metrics"
)

func ExampleBootstrap() {
	// A real service lets Bootstrap block on SIGINT or SIGTERM. The example
	// cancels the context from inside the bind function so it terminates.
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	var wg sync.WaitGroup

	shutdown := make(chan struct{})

	// BindFunc is where the application registers its own components: HTTP
	// servers, database connections, background workers. It receives the
	// application context, logger, and metrics client already wired.
	bindFn := func(ctx context.Context, _ *slog.Logger, mtr metrics.Client) error {
		worker(ctx, &wg, shutdown)

		mtr.IncErrorCounter("startup", "bind", "0")

		fmt.Println("application wired")

		// Stand-in for the signal that would normally end the process.
		cancel()

		return nil
	}

	// Bootstrap returns once every registered dependant has finished, or
	// with an error wrapping ErrShutdownTimeout if they exceed the budget.
	err := bootstrap.Bootstrap(
		bindFn,
		bootstrap.WithContext(ctx),
		bootstrap.WithShutdownSignalChan(shutdown),
		bootstrap.WithShutdownWaitGroup(&wg),
		bootstrap.WithShutdownTimeout(5*time.Second),
		bootstrap.WithCreateMetricsClientFunc(func() (metrics.Client, error) {
			return &metrics.Default{}, nil
		}),
	)

	fmt.Println("bootstrap returned:", err)

	// Output:
	// application wired
	// worker stopped
	// bootstrap returned: <nil>
}

// worker is a background task participating in graceful shutdown: it joins the
// wait group so Bootstrap blocks for it, and stops when either the shutdown
// channel closes or the context is canceled.
func worker(ctx context.Context, wg *sync.WaitGroup, shutdown chan struct{}) {
	wg.Go(func() {
		select {
		case <-shutdown:
		case <-ctx.Done():
		}

		fmt.Println("worker stopped")
	})
}
