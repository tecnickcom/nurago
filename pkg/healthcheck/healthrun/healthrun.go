/*
Package healthrun runs dependency probes concurrently and reports the outcome of
each one, without any transport dependency.

It defines the check model shared by the health endpoints of this library:
  - [HealthChecker]: check contract (`HealthCheck(context.Context) error`),
    with [HealthCheckFunc] to adapt a plain function
  - [HealthCheck]: ID + checker registration unit
  - [Runner]: runs the checks in parallel and returns their outcomes keyed by
    check ID

[github.com/tecnickcom/nurago/pkg/healthcheck] serves the outcomes over HTTP
and [github.com/tecnickcom/nurago/pkg/grpcserver] over the gRPC health service.
The healthcheck types are aliases of the types of this package, so the same
checks can be registered with both.

# Run Semantics

  - checks execute in parallel, one goroutine per check
  - a checker panic is recovered, logged, and reported as [ErrCheckPanic]
  - a nil checker is reported as [ErrNoChecker]
  - with [WithTimeout], checks that overrun are reported as [ErrCheckTimeout]

Check IDs should be unique and non-empty: outcomes are keyed by ID, so duplicate
or empty IDs collapse into a single entry (a warning is logged at construction).

# When To Use

  - A non-HTTP transport must report the status of several dependencies.
  - A service must avoid the HTTP dependencies of pkg/healthcheck.
*/
package healthrun

import (
	"context"
	"errors"
)

var (
	// ErrCheckTimeout is reported for a check that did not return before the
	// configured timeout (or the run context) elapsed. See [WithTimeout].
	ErrCheckTimeout = errors.New("healthcheck timed out")

	// ErrCheckPanic is reported in place of a checker panic value. The panic and
	// its stack trace are logged through the runner logger instead of being
	// reported.
	ErrCheckPanic = errors.New("healthcheck panicked")

	// ErrNoChecker is reported for a registered check whose Checker is nil. It is
	// a configuration error (also warned at construction) reported as a plain
	// failure rather than a recovered nil-pointer panic.
	ErrNoChecker = errors.New("healthcheck has no checker")
)

// HealthChecker defines a single health probe operation.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// HealthCheckFunc adapts a plain function to the [HealthChecker] interface,
// mirroring the http.HandlerFunc pattern, so a function-based probe can be
// registered without a wrapper type.
type HealthCheckFunc func(ctx context.Context) error

// HealthCheck calls f(ctx).
func (f HealthCheckFunc) HealthCheck(ctx context.Context) error {
	return f(ctx)
}

// HealthCheck describes one registered probe and its unique identifier.
type HealthCheck struct {
	// ID is a unique identifier for the healthcheck.
	ID string

	// Checker is the function used to perform the healthchecks.
	Checker HealthChecker
}

// New creates a HealthCheck registration entry.
//
// The ID should be unique and non-empty within a runner, as outcomes are
// aggregated by ID.
func New(id string, checker HealthChecker) HealthCheck {
	return HealthCheck{
		ID:      id,
		Checker: checker,
	}
}
