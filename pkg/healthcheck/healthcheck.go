/*
Package healthcheck runs dependency probes concurrently and aggregates them into
a single HTTP health endpoint.

It standardizes health probing around three pieces:
  - [HealthChecker]: check contract (`HealthCheck(context.Context) error`),
    with [HealthCheckFunc] to adapt a plain function
  - [HealthCheck]: ID + checker registration unit
  - [Handler]: HTTP aggregator that runs checks concurrently and writes a
    combined result

The check model and the parallel execution are provided by
[github.com/tecnickcom/nurago/pkg/healthcheck/healthrun], which has no HTTP
dependency: [HealthChecker], [HealthCheckFunc] and [HealthCheck] are aliases
of its types.

The default handler response is JSON and maps each check ID to either "OK" or
the check error message. [Handler.Run] returns the same per-check outcomes
without writing a response, for non-HTTP transports such as the gRPC health
service of github.com/tecnickcom/nurago/pkg/grpcserver.

The client packages in this library expose a HealthCheck method that satisfies
[HealthChecker] directly, so they register without an adapter:
[github.com/tecnickcom/nurago/pkg/redis],
[github.com/tecnickcom/nurago/pkg/valkey],
[github.com/tecnickcom/nurago/pkg/sqs],
[github.com/tecnickcom/nurago/pkg/s3],
[github.com/tecnickcom/nurago/pkg/sqlconn],
[github.com/tecnickcom/nurago/pkg/ipify] and
[github.com/tecnickcom/nurago/pkg/slack].

# Aggregation Semantics

  - checks execute in parallel
  - overall HTTP status is 200 when all checks pass
  - overall HTTP status is 503 when any check fails
  - response payload always includes per-check results
  - a checker panic is recovered, logged, and reported as a failed check
  - with [WithTimeout], checks that overrun are reported as failed

Check IDs should be unique and non-empty: results are keyed by ID, so duplicate
or empty IDs collapse into a single entry (a warning is logged at construction).

Result-writing behavior is customizable via [WithResultWriter] to integrate
custom envelopes (for example JSendX) while keeping the execution model
unchanged.

# HTTP Probe Helper

[CheckHTTPStatus] is a helper for external HTTP dependencies. It supports
context timeout control and request customization via [WithConfigureRequest].

For an implementation example, see examples/service/internal/cli/bind.go.

# When To Use

  - A readiness endpoint must report the status of several dependencies
    individually.
  - You want each probe bounded by a timeout so one hung dependency does not
    hang the endpoint.
  - Your clients already expose a HealthCheck(ctx) error method.
*/
package healthcheck

import (
	"github.com/tecnickcom/nurago/pkg/healthcheck/healthrun"
)

// HealthChecker defines a single health probe operation. It is an alias of
// [healthrun.HealthChecker].
type HealthChecker = healthrun.HealthChecker

// HealthCheckFunc adapts a plain function to the [HealthChecker] interface,
// mirroring the http.HandlerFunc pattern, so a function-based probe (such as one
// calling [CheckHTTPStatus]) can be registered without a wrapper type. It is an
// alias of [healthrun.HealthCheckFunc]:
//
//	healthcheck.New("upstream", healthcheck.HealthCheckFunc(func(ctx context.Context) error {
//		return healthcheck.CheckHTTPStatus(ctx, client, http.MethodGet, url, http.StatusOK, 2*time.Second)
//	}))
type HealthCheckFunc = healthrun.HealthCheckFunc

// HealthCheck describes one registered probe and its unique identifier. It is
// an alias of [healthrun.HealthCheck].
type HealthCheck = healthrun.HealthCheck

// New creates a HealthCheck registration entry.
//
// It binds a stable check ID to a HealthChecker implementation so handlers can
// execute and report results consistently. The ID should be unique and
// non-empty within a handler, as results are aggregated by ID.
func New(id string, checker HealthChecker) HealthCheck {
	return healthrun.New(id, checker)
}
