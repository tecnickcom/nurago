package healthrun_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
)

func ExampleRunner_Run() {
	checks := []healthrun.HealthCheck{
		healthrun.New("database", healthrun.HealthCheckFunc(func(_ context.Context) error {
			return errors.New("connection refused")
		})),
		healthrun.New("cache", healthrun.HealthCheckFunc(func(_ context.Context) error {
			return nil
		})),
	}

	runner := healthrun.NewRunner(checks, healthrun.WithTimeout(time.Second))

	results := runner.Run(context.TODO())

	fmt.Println(results["cache"])
	fmt.Println(results["database"])

	// Output:
	// <nil>
	// connection refused
}
