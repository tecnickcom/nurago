/*
Package main runs the nuragoexample service.

# Overview

nuragoexample is a reference service built with nurago. It demonstrates how to
bootstrap a Go service with consistent configuration, structured logging,
metrics, health checks, and graceful shutdown.

# Runtime Topology

By default, the process starts three HTTP servers and, when
`servers.grpc.enabled` is true, a gRPC server:

- Monitoring server on :8071
- Private API server on :8072
- Public API server on :8073
- gRPC server on :8074

The monitoring server exposes default operational endpoints (for example
index, ping, status, metrics, and profiling routes provided by nurago's
HTTP server package). The private and public APIs both expose a ping route
and the example /uid route.

The gRPC server exposes the item.v1.ItemService API (defined in
proto/item/v1/item.proto) and the standard grpc.health.v1.Health service,
backed by the same health checks as the HTTP status route. Server reflection
is enabled with `servers.grpc.reflection`.

The /uid route returns a UUIDv7 value, which is useful as an example of
application routes that can be attached to different exposure boundaries
(internal vs public).

# Configuration Model

Configuration is loaded through `nurago/pkg/config` and supports:

- Local configuration files
- Remote configuration via a base64 environment variable payload (the "envvar" provider)
- Environment variable overrides with the `NURAGOEXAMPLE` prefix

Other remote configuration backends (for example Consul or Etcd) can be
enabled by registering a loader with the config.WithRemoteLoader option;
see doc/CONFIG.md for details.

Default ports and timeouts are defined in the CLI config package, and the
config schema supports enabling or disabling:

- The service handlers (`enabled`)
- Database connectivity (`db.enabled`)
- The gRPC server (`servers.grpc.enabled`)

When `db.enabled` is true, main and read connections are created, instrumented,
and included in health checks.

# Features

  - Bootstrap lifecycle: startup and shutdown are coordinated with a shared
    wait group and shutdown signal channel.
  - Structured logging: configurable format and level with build metadata
    (`program`, `version`, `release`) attached to each record.
  - HTTP stack: request instrumentation middleware, Prometheus metrics
    exposure, and standard failure handlers.
  - gRPC stack: the item API served from the same business service as the
    REST routes, with request logging, panic recovery, default deadlines,
    and Prometheus metrics exported by the monitoring server.
  - Health reporting: default status endpoint can be upgraded to dependency-
    aware health checks when optional components are enabled.
  - Trace propagation: outbound clients and servers use a shared trace header
    for request correlation.

Public, private, and monitoring traffic are isolated by exposure boundary, and
service wiring lives in `internal/cli/bind.go`.

# Command-Line Flags

The root command supports common runtime overrides:

- `-c`, `--configDir`: add a configuration directory to the search list
- `-f`, `--logFormat`: set log format (`CONSOLE` or `JSON`)
- `-o`, `--logLevel`: set log level

The version subcommand prints the compiled program version.
*/
package main
