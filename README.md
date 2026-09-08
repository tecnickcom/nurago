# nurago

> [!IMPORTANT]
> **This project was previously named [gogen](https://github.com/tecnickcom/gogen)**: same library, same packages, new name. The old `github.com/tecnickcom/gogen` module path is **deprecated**; all existing versions remain available via the Go module proxy. To migrate:
>
> ```bash
> go get github.com/tecnickcom/nurago@latest
> find . -name '*.go' -exec sed -i 's|github.com/tecnickcom/gogen|github.com/tecnickcom/nurago|g' {} +
> go mod tidy
> ```
>
> Full instructions: [Migration from gogen](https://nurago.org/docs/migration-from-gogen/).

[![GitHub Release](https://img.shields.io/github/v/release/tecnickcom/nurago)](https://github.com/tecnickcom/nurago/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/tecnickcom/nurago.svg)](https://pkg.go.dev/github.com/tecnickcom/nurago)
[![Coverage Status](https://coveralls.io/repos/github/tecnickcom/nurago/badge.svg?branch=main)](https://coveralls.io/github/tecnickcom/nurago?branch=main)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/11517/badge)](https://www.bestpractices.dev/projects/11517)
[![Sponsor on GitHub](https://img.shields.io/badge/sponsor-github-EA4AAA.svg?logo=githubsponsors&logoColor=white)](https://github.com/sponsors/tecnickcom)

If this project is useful to you, please consider [supporting development via GitHub Sponsors](https://github.com/sponsors/tecnickcom).

**`nurago` is a collection of independent Go packages for building backend services**: retries and exponential backoff, HTTP client and server, OpenTelemetry and Prometheus instrumentation, structured logging and log redaction, Argon2id password hashing, JWT, Redis, Valkey, Kafka, AWS S3 and SQS, SQL connection and transaction handling, caching, validation, and configuration loading.

Each package is imported on its own and pulls only the dependencies it reaches, so adopting one does not commit you to the rest. Most reach none at all: see [Dependency Footprint](#dependency-footprint).

*Why "nurago"?* From *nuraghe* + Go: the Bronze Age Sardinian stone towers, built without mortar, around 7,000 of which still stand after 3,500 years.

Website: [nurago.org](https://nurago.org/)

Source documentation: [pkg.go.dev/github.com/tecnickcom/nurago](https://pkg.go.dev/github.com/tecnickcom/nurago)

## Table of Contents

1. [Installation](#installation)
2. [Documentation](#documentation)
3. [Dependency Footprint](#dependency-footprint)
4. [API Stability](#api-stability)
5. [Package Catalog](#package-catalog)
6. [Developers Quick Start](#developers-quick-start)
7. [Running All Tests](#running-all-tests)
8. [How To Create a New Web Service](#how-to-create-a-new-web-service)
9. [Contributing](#contributing)

## Installation

```bash
go get github.com/tecnickcom/nurago
```

Import the packages you need, individually:

```go
import (
    "github.com/tecnickcom/nurago/pkg/backoff"
    "github.com/tecnickcom/nurago/pkg/redact"
)
```

Every package directory has its own `README.md` with a runnable example and
guidance on when the package is the right choice.

The repository also includes a generator path that scaffolds a complete web
service from a configuration file:

```bash
make project CONFIG=project.cfg
```

## Documentation

Guides, package pages, and articles are published on
[nurago.org](https://nurago.org/):

- [Packages](https://nurago.org/packages/) - all packages, grouped by what they do.
- [Getting started](https://nurago.org/docs/getting-started/) - installing, importing a package, and wiring a service lifecycle.
- [Service scaffolding](https://nurago.org/docs/service-scaffolding/) - generating a complete web service with `make project`.
- [Dependency footprint](https://nurago.org/docs/dependency-footprint/) - what each import pulls into the build, and how to verify it.
- [Observability](https://nurago.org/docs/observability/) - logging, metrics backends, trace IDs, and log redaction.
- [Resilience](https://nurago.org/docs/resilience/) - retries, backoff and jitter, periodic work, DNS caching.
- [Security](https://nurago.org/docs/security/) - password storage, breach checks, JWT, encryption, private endpoints.
- [Data and messaging](https://nurago.org/docs/data-and-messaging/) - SQL, locking, Redis, Valkey, Kafka, S3, SQS.
- [Testing](https://nurago.org/docs/testing/) - test helpers, injectable clients, mocks, and the QA pipeline.
- [Migration from gogen](https://nurago.org/docs/migration-from-gogen/) - moving off the deprecated `github.com/tecnickcom/gogen` module path.
- [Development](https://nurago.org/docs/development/) - Makefile workflow, linting, coverage, benchmarks, Docker.
- [AI coding assistants](https://nurago.org/docs/ai-assistants/) - `llms.txt`, MCP retrieval, project rules, and the facts to pin.
- [Features](https://nurago.org/features/) - capability index, grouped by area.
- [Comparison](https://nurago.org/comparison/) - nurago next to a framework, an internal library, and the standard library.
- [Articles](https://nurago.org/articles/) - in-depth articles on the design of individual packages.

A machine-readable index of every package is in [llms.txt](llms.txt), also
served at [nurago.org/llms.txt](https://nurago.org/llms.txt).

## Dependency Footprint

Packages are imported individually and pull only what they reach. Importing
`pkg/backoff` does not add the AWS SDK, Kafka, Redis, or OpenTelemetry to your
build, even though other packages in this module require them. `go mod tidy`
keeps only the modules your imports actually reach.

<!-- gendoc:deps:start -->

**40 of the 70 packages reach no external module at all**, using nothing
beyond the Go standard library:

`backoff`, `countrycode`, `countryphone`, `decint`, `dnscache`, `encode`,
`encrypt`, `enumbitmap`, `enumcache`, `enumdb`, `errutil`, `filter`,
`httpclient`, `httpretrier`, `ipify`, `logutil`, `maputil`, `metrics`,
`mysqllock`, `numtrie`, `paging`, `periodic`, `phonekeypad`, `random`,
`redact`, `retrier`, `sfcache`, `sliceutil`, `sqlconn`, `sqltransaction`,
`sqlutil`, `stringmetric`, `strsplit`, `threadsafe`, `timeutil`, `traceid`,
`tsmap`, `tsslice`, `typeutil`, `uhex`.

Each package README lists its own footprint. Verify any of them with:

```bash
go list -deps -f '{{if .Module}}{{.Module.Path}}{{end}}' \
  github.com/tecnickcom/nurago/pkg/backoff | sort -u
```

<!-- gendoc:deps:end -->

## API Stability

`nurago` follows [semantic versioning](https://semver.org/). The module is at
v1 and the exported API of every `pkg/` package is stable: no breaking change
will be made to an exported symbol within v1. Additions are released as minor
versions, fixes as patch versions, and any breaking change would require a v2
module path.

The release cadence is frequent because dependency updates and additive changes
ship as soon as they are ready. A high patch number does not indicate churn in
the API.

## Package Catalog

Each package also has a page on
[nurago.org/packages](https://nurago.org/packages/).

<!-- gendoc:catalog:start -->

All 70 packages, each linking to its own README. The description is the
first sentence of the package documentation.

- [awsopt](pkg/awsopt) - configures the aws-sdk-go-v2 library consistently across multiple AWS service clients. It centralizes config.LoadOptionsFunc calls into a composable Options slice that can be built once and handed to any AWS-based package in this library.
- [awssecretcache](pkg/awssecretcache) - provides a local, thread-safe, fixed-size cache for AWS Secrets Manager lookups, with single-flight deduplication.
- [backoff](pkg/backoff) - computes successive retry delays with exponential growth, a bounded maximum, and random jitter.
- [bootstrap](pkg/bootstrap) - wires together the core infrastructure of a Go service: context lifecycle, structured logging, metrics collection, OS signal handling, and graceful shutdown, in a single function call.
- [config](pkg/config) - provides configuration bootstrap for Go services built on top of Viper.
- [countrycode](pkg/countrycode) - provides access to ISO-3166 country metadata.
- [countryphone](pkg/countryphone) - resolves international phone number prefixes into country and regional metadata.
- [decint](pkg/decint) - provides utility functions to parse and represent decimal values as fixed-point integers with a defined precision.
- [devlake](pkg/devlake) - provides a Go client for the DevLake Webhook API.
- [dnscache](pkg/dnscache) - provides a local DNS cache that is safe for concurrent use, bounded in size, and uses single-flight request collapsing to avoid duplicate lookups.
- [encode](pkg/encode) - serializes and deserializes values crossing system boundaries such as databases, queues, caches, and RPC payloads.
- [encrypt](pkg/encrypt) - encrypts and decrypts data for transport and storage using AES-GCM authenticated encryption.
- [enumbitmap](pkg/enumbitmap) - encodes a set of enumeration values as an integer bitmap and decodes it back.
- [enumcache](pkg/enumcache) - provides thread-safe storage and lookup for enumeration name and ID mappings.
- [enumdb](pkg/enumdb) - loads enumeration sets from relational database tables into thread-safe enum caches.
- [errutil](pkg/errutil) - annotates errors with caller location, joins cleanup failures onto an existing error, and enumerates the errors inside an errors.Join value.
- [filter](pkg/filter) - provides declarative, rule-based filtering for in-memory slices. It evaluates structured Rule expressions against slice elements and filters the slice in place.
- [healthcheck](pkg/healthcheck) - runs dependency probes concurrently and aggregates them into a single HTTP health endpoint.
- [httpclient](pkg/httpclient) - provides a configurable outbound HTTP client with trace propagation and structured request/response logging.
- [httpretrier](pkg/httpretrier) - provides configurable retry execution for outbound HTTP requests.
- [httpreverseproxy](pkg/httpreverseproxy) - provides a reverse-proxy client built on top of net/http/httputil.ReverseProxy.
- [httpserver](pkg/httpserver) - provides a configurable HTTP server bootstrap for Go services.
- [httputil](pkg/httputil) - provides HTTP request/response primitives for Go services built on top of net/http.
- [jsendx](pkg/httputil/jsendx) - implements an extended JSend response envelope for HTTP APIs.
- [ipify](pkg/ipify) - provides a small client to resolve the current instance public IP address using the ipify service (https://www.ipify.org/).
- [jirasrv](pkg/jirasrv) - provides a typed HTTP client foundation for Jira Server REST integrations.
- [jwt](pkg/jwt) - provides an HTTP-oriented JWT authentication helper for username/password login flows: validate user credentials, issue short-lived signed JWTs, authorize protected endpoints from an Authorization header, and optionally renew tokens near expiration.
- [kafka](pkg/kafka) - provides a pure-Go API for producing and consuming Apache Kafka messages. It requires no CGO and no system librdkafka installation.
- [logsrv](pkg/logsrv) - provides a zerolog backend exposed through the standard log/slog API.
- [logutil](pkg/logutil) - provides configuration-driven logging utilities built around Go's standard log/slog package.
- [maputil](pkg/maputil) - filters, maps, reduces, and inverts Go maps with generic functions.
- [metrics](pkg/metrics) - defines a backend-agnostic instrumentation contract for Go services.
- [opentel](pkg/metrics/opentel) - implements github.com/tecnickcom/nurago/pkg/metrics.Client using OpenTelemetry for both metrics and tracing.
- [prometheus](pkg/metrics/prometheus) - implements github.com/tecnickcom/nurago/pkg/metrics.Client using the Prometheus client ecosystem.
- [statsd](pkg/metrics/statsd) - implements github.com/tecnickcom/nurago/pkg/metrics.Client using the StatsD protocol.
- [mysqllock](pkg/mysqllock) - provides process-distributed mutual exclusion using MySQL's named lock primitives GET_LOCK and RELEASE_LOCK.
- [numtrie](pkg/numtrie) - provides a generic, digit-indexed trie (prefix tree) for associating values of any type with numerical keys, with built-in support for partial/prefix matching and alphabetical (vanity) phone-number keys.
- [paging](pkg/paging) - computes pagination metadata (current page, total pages, previous/next page numbers, and SQL OFFSET/LIMIT values) from three inputs: current page number, page size, and total item count.
- [passwordhash](pkg/passwordhash) - provides OWASP-compliant password hashing and verification using the Argon2id algorithm (RFC 9106), with an optional AES-GCM encryption layer (peppered hashing) for defense in depth.
- [passwordpwned](pkg/passwordpwned) - checks whether a password has appeared in a known data breach, using the Have I Been Pwned (HIBP) Pwned Passwords API v3 (https://haveibeenpwned.com/API/v3#PwnedPasswords).
- [periodic](pkg/periodic) - schedules a task function to run repeatedly at a fixed interval, with optional random jitter and a per-invocation context timeout.
- [phonekeypad](pkg/phonekeypad) - converts alphabetic strings and phone number literals to their numeric equivalents on a standard 12-key telephony keypad (ITU E.161 / ITU T.9).
- [profiling](pkg/profiling) - bridges Go's built-in net/http/pprof profiling tool and the httprouter request router, allowing all pprof endpoints to be served through a single wildcard route without manual per-handler registration.
- [random](pkg/random) - provides utility functions for generating random bytes, numeric identifiers, UID/UUID values, hexadecimal/base36 IDs, and configurable random strings.
- [redact](pkg/redact) - removes secrets from log lines and HTTP dumps before they are emitted.
- [redis](pkg/redis) - wraps go-redis for key/value storage, Pub/Sub messaging, typed payload encoding, and connection health checks.
- [retrier](pkg/retrier) - provides a configurable retry engine for executing a task function with backoff, jitter, and per-attempt timeouts.
- [s3](pkg/s3) - uploads, downloads, lists, and deletes S3 bucket objects through the AWS SDK v2 S3 client.
- [sfcache](pkg/sfcache) - provides a local, thread-safe, fixed-size cache for expensive lookups with single-flight deduplication.
- [slack](pkg/slack) - provides a client for sending messages to Slack via Incoming Webhooks.
- [sleuth](pkg/sleuth) - provides a Go client for the Sleuth.io API, covering common write-side integrations for delivery metrics and operational signal ingestion.
- [sliceutil](pkg/sliceutil) - filters, maps, and reduces slices with generic functions, and summarizes numeric slices with descriptive statistics.
- [sqlconn](pkg/sqlconn) - manages a database/sql connection lifecycle in long-running Go services: applying pool limits, verifying connectivity, exposing health checks, and closing the connection on shutdown signals.
- [sqltransaction](pkg/sqltransaction) - executes business logic inside a transaction with begin/commit/rollback control flow and consistent error handling.
- [sqlutil](pkg/sqlutil) - quotes identifiers and string literals when generating SQL query fragments dynamically.
- [sqlxtransaction](pkg/sqlxtransaction) - handles begin/commit/rollback control flow around business logic executed inside a sqlx transaction.
- [sqs](pkg/sqs) - wraps github.com/aws/aws-sdk-go-v2/service/sqs with an API that covers the common queue workflow: send, receive, decode, acknowledge (delete), and health-check.
- [stringkey](pkg/stringkey) - derives a stable, compact, non-cryptographic key from multiple text fields for lookup, deduplication, and idempotency-style identifiers.
- [stringmetric](pkg/stringmetric) - provides string distance functions for approximate text matching, comparison, and fuzzy search.
- [strsplit](pkg/strsplit) - splits strings into bounded-size chunks without breaking Unicode characters, keeping human-readable boundaries (spaces, punctuation, and newlines).
- [testutil](pkg/testutil) - provides test-only helpers for forcing I/O failures on demand, capturing process output, bootstrapping HTTP handlers, and normalizing time-variant values in assertions.
- [threadsafe](pkg/threadsafe) - defines lock interfaces for building reusable, goroutine-safe data structures and helpers without hard-coding a concrete lock type.
- [tsmap](pkg/threadsafe/tsmap) - reads and writes maps shared across goroutines, taking a caller-supplied lock at every call site.
- [tsslice](pkg/threadsafe/tsslice) - reads and writes slices shared across goroutines, taking a caller-supplied lock at every access.
- [timeutil](pkg/timeutil) - provides two JSON-friendly time types. The standard library's time.Time marshals as RFC-3339 only, and time.Duration marshals as a raw nanosecond integer, which mismatch APIs that expect human-readable strings like "1h30m" or "2023-01-02T15:04:05Z". DateTime and Duration marshal to and from such strings, and the datetime format is selected by a type parameter checked at compile time.
- [traceid](pkg/traceid) - captures a request-scoped trace ID at the service boundary: it reads the ID from an inbound HTTP header, propagates it through the context.Context for the lifetime of the request, and writes it back into outbound HTTP headers when calling downstream services, without coupling business logic to any particular tracing framework.
- [typeutil](pkg/typeutil) - detects nil through interfaces, obtains zero values generically, dereferences pointers safely, and converts booleans to integers without a branch.
- [uhex](pkg/uhex) - provides fixed-width, lowercase hexadecimal encoders for unsigned integers and fixed-size byte arrays.
- [validator](pkg/validator) - wraps https://github.com/go-playground/validator and adds custom validation rules, a template-based error translation engine, and a functional-options API.
- [valkey](pkg/valkey) - wraps the valkey-go client (https://github.com/valkey-io/valkey-go) for Valkey (https://valkey.io), a Redis-compatible in-memory data store. It covers key/value storage, typed data serialization, and Pub/Sub messaging behind a single Client type.

<!-- gendoc:catalog:end -->

## Developers Quick Start

Requirements:

- Go 1.26.0 or later (the minimum declared in `go.mod`; any newer release works)

Clone and validate the repository:

```bash
git clone https://github.com/tecnickcom/nurago.git
cd nurago
make x
```

The `Makefile` provides a Linux-friendly workflow for build/test operations. Generated artifacts and reports are written to `target/`.

To run the same process in Docker:

```bash
make dbuild
```

This uses `resources/docker/Dockerfile.dev`.

List all available commands:

```bash
make help
```

## Running All Tests

Before committing, run:

```bash
make x
```

Or run tests/build inside Docker:

```bash
make dbuild
```

## How To Create a New Web Service

The directory `examples/service` contains a sample web service built with `nurago`.

To scaffold a new project:

### Clone the nurago repository

```bash
$ git clone https://github.com/tecnickcom/nurago.git

Cloning into 'nurago'...
```

### Move to the cloned project directory

```bash
$ cd nurago/
```

### List available Make targets

```bash
$ make

# nurago Makefile.
# GOPATH=/home/demo/GO
# The following commands are available:
#
#   make x              : Test and build everything from scratch
#   make clean          : Remove any build artifact
#   make coverage       : Generate the coverage report
#   make dbuild         : Build everything inside a Docker container
#   make deps           : Get dependencies
#   make dockerdev      : Build a base development Docker image
#   make ensuretarget   : Create the target directories if missing
#   make example        : Build and test the service example
#   make format         : Format the source code
#   make gendoc         : Generate the documentation derived from the source (READMEs, llms.txt)
#   make gendoccheck    : Check that the generated documentation is up to date
#   make generate       : Generate Go code automatically
#   make govulncheck    : Check dependencies for known vulnerabilities
#   make linter         : Check code against multiple linters
#   make mod            : Download dependencies
#   make project        : Generate a new project from the example using the data set via CONFIG=project.cfg
#   make qa             : Run all tests and static analysis tools
#   make tag            : Tag the Git repository
#   make test           : Run unit tests
#   make bench          : Run benchmarks (without -race or coverage) and store the results for comparison (target/report/bench.txt)
#   make benchbase      : Run base benchmarks (without -race or coverage) and store the results for comparison (target/report/bench_base.txt)
#   make benchcmp       : Compare benchmark allocation counts against a base git ref and fail on regressions (set BENCHBASE=ref, default main)
#   make benchgate      : Compare benchmark allocation counts against a baseline file and fail on regressions (set BASELINE=path/to/baseline.txt)
#   make gotools        : Get the go tools
#   make updateall      : Update everything
#   make updatego       : Update Go version
#   make updatelint     : Update golangci-lint version
#   make updatemod      : Update dependencies
#   make version        : Update this library version in the examples
#   make versionup      : Increase the patch number in the VERSION file
#
# To run the full test and build flow from scratch, use:
#     make x
```

### Run the full test and build pipeline

```bash
$ make x

# DEVMODE=LOCAL make version format clean mod deps generate gendoc qa example

# 1. make version       : Update this library version in the examples
# 2. make format        : Format the source code
# 3. make clean         : Remove any build artifact
# 4. make mod           : Download dependencies
# 5. make deps          : Get dependencies
# 6. make generate      : Generate Go code automatically (test mocks)
# 7. make gendoc        : Regenerate the package READMEs and llms.txt from the source
# 8. make qa            : Run all tests and static analysis tools
    # 8.1. make linter      : Check the code with multiple linters (golangci/golangci-lint)
    # 8.2. make test        : Run unit tests (go test)
    # 8.3. make coverage    : Generate the coverage report (/target/report/coverage.html)
# 9. make example       : Build and test the service example
    # 9.1. make clean       : Remove any build artifact
    # 9.2. make mod         : Download dependencies
    # 9.3. make deps        : Get dependencies
    # 9.4. make gendoc      : Generate static documentation from /doc/src (gomplate)
    # 9.5. make generate    : Generate Go code automatically (test mocks)
    # 9.6. make qa          : Run all tests and static analysis tools
        # 9.6.1. make linter    : Check the code with multiple linters (golangci/golangci-lint)
        # 9.6.2. make confcheck : Check the configuration files (jv)
        # 9.6.3. make test      : Run unit tests (go test)
        # 9.6.4. make coverage  : Generate the coverage report (target/report/coverage.html)
    # 8.7. make build       : Compile the application (go build > target/usr/bin/nuragoexample)
```

### Create a new project from the examples/service template

#### Customize the project configuration file

```bash
$ cp project.cfg myproject.cfg

$ nano myproject.cfg
```

#### Generate the project

```bash
$ make project CONFIG=myproject.cfg

# Project created at target/github.com/test/dummy
```

#### Move the project to a new location

```bash
$ mv target/github.com/test/dummy ~/GO/src/myproject/
```

#### Run the full test suite on the new project

```bash
$ cd ~/GO/src/myproject/

$ make x

# DEVMODE=LOCAL make format clean mod deps gendoc generate qa build docker dockertest

#  1. make format      : Format the source code
#  2. make clean       : Remove any build artifact
#  3. make mod         : Download dependencies
#  4. make deps        : Get dependencies
#  5. make gendoc      : Generate static documentation from /doc/src (gomplate)
#  6. make generate    : Generate Go code automatically (test mocks)
#  7. make qa          : Run all tests and static analysis tools
    #  7.1. make linter    : Check the code with multiple linters (golangci/golangci-lint)
    #  7.2. make confcheck : Check the configuration files (jv)
    #  7.3. make test      : Run unit tests (go test)
    #  7.4. make coverage  : Generate the coverage report (target/report/coverage.html)
#  8. make build       : Compile the application (go build > target/usr/bin/nuragoexample)
#  9. make docker      : Build a scratch Docker container to run this service
# 10. make dockertest  : Test the newly built Docker container in an ephemeral Docker Compose environment
    # 10.1. DEPLOY_ENV=int make openapitest apitest
        # 10.1.1. make openapitest : Test the OpenAPI specification with randomly generated Schemathesis tests
        # 10.1.2. make apitest     : Execute API tests with venom
```

## Contributing

Contributions are welcome. Please review [CONTRIBUTING.md](https://github.com/tecnickcom/nurago/blob/main/CONTRIBUTING.md) and the [development guide](https://nurago.org/docs/development/) before opening a pull request.
