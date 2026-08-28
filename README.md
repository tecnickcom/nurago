# nurago

> [!IMPORTANT]
> **This project was previously named [gogen](https://github.com/tecnickcom/gogen)** — same library, same packages, new name. The old `github.com/tecnickcom/gogen` module path is **deprecated**; all existing versions remain available via the Go module proxy. To migrate:
>
> ```bash
> go get github.com/tecnickcom/nurago@latest
> find . -name '*.go' -exec sed -i 's|github.com/tecnickcom/gogen|github.com/tecnickcom/nurago|g' {} +
> go mod tidy
> ```

[![GitHub Release](https://img.shields.io/github/v/release/tecnickcom/nurago)](https://github.com/tecnickcom/nurago/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/tecnickcom/nurago.svg)](https://pkg.go.dev/github.com/tecnickcom/nurago)
[![Coverage Status](https://coveralls.io/repos/github/tecnickcom/nurago/badge.svg?branch=main)](https://coveralls.io/github/tecnickcom/nurago?branch=main)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/11517/badge)](https://www.bestpractices.dev/projects/11517)
[![Sponsor on GitHub](https://img.shields.io/badge/sponsor-github-EA4AAA.svg?logo=githubsponsors&logoColor=white)](https://github.com/sponsors/tecnickcom)

If this project is useful to you, please consider [supporting development via GitHub Sponsors](https://github.com/sponsors/tecnickcom).

**`nurago` is a production-oriented collection of modular, reusable Go packages for building services and infrastructure code.**

It solves a common problem in backend teams: repeatedly re-implementing the same foundational components (configuration loading, retries, health checks, logging, metrics, AWS integration, validation, caching, and more) across multiple repositories.

Instead of assembling and maintaining ad-hoc helpers per project, you can adopt tested packages with consistent patterns.

*Why "nurago"?* From *nuraghe* + Go: the Bronze Age Sardinian stone towers, built without mortar, ~7,000 of which still stand after 3,500 years — modular, stone-solid foundations with no lock-in, which is exactly what this library aims to be.

Source documentation: [pkg.go.dev/github.com/tecnickcom/nurago](https://pkg.go.dev/github.com/tecnickcom/nurago)

## Table of Contents

1. [Why nurago](#why-nurago)
2. [Feature Highlights](#feature-highlights)
3. [Benefits Summary](#benefits-summary)
4. [Package Catalog](#package-catalog)
5. [Developers Quick Start](#developers-quick-start)
6. [Running All Tests](#running-all-tests)
7. [How To Create a New Web Service](#how-to-create-a-new-web-service)
8. [Contributing](#contributing)

## Why nurago

`nurago` is a good fit for Go teams that want:

- A consistent utility layer across services
- Reusable packages rather than project-specific scripts
- A modular import model without framework lock-in
- A practical example service to accelerate onboarding
- A Makefile-driven workflow for test/build/scaffolding

It also includes a generator path:

```bash
make project CONFIG=project.cfg
```

This scaffolds a new web service from the provided configuration.

## Feature Highlights

- Broad package coverage for day-to-day service needs, reducing dependency sprawl and avoiding repeated boilerplate utility code.

- Production-focused building blocks for HTTP, retries, observability, data stores, and AWS, helping teams ship services faster with less glue code.

- Consistent conventions across packages that make code reviews easier, maintenance simpler, and APIs more predictable.

- A testing-first repository culture that supports safer refactoring and more reliable behavior over time.

- Built-in project scaffolding and a runnable example service to speed up project bootstrap and provide a clearer implementation reference.

## Benefits Summary

- Faster development cycles for new services
- Less duplicated utility code across repositories
- Better consistency in operational concerns (logging, metrics, health, tracing)
- Cleaner architecture through package-level composition
- Easier onboarding for engineers joining an existing Go platform

## Package Catalog

`nurago` offers a comprehensive set of well-tested packages.

- [awsopt](pkg/awsopt) - Utilities for configuring common AWS options with the aws-sdk-go-v2 library. `aws`, `configuration`
- [awssecretcache](pkg/awssecretcache) - Client for retrieving and caching secrets from AWS Secrets Manager. `aws`, `secrets`, `caching`
- [backoff](pkg/backoff) - Exponential backoff delay schedule with jitter. `retry`, `backoff`, `jitter`
- [bootstrap](pkg/bootstrap) - Helpers for application bootstrap and initialization. `bootstrap`, `initialization`
- [config](pkg/config) - Utilities for configuration loading and management. `configuration`
- [countrycode](pkg/countrycode) - Functions for country code lookup and validation. `geolocation`, `validation`
- [countryphone](pkg/countryphone) - Phone number parsing and country association. `phone`, `geolocation`, `parsing`
- [decint](pkg/decint) - Helpers for parsing and formatting decimal integers. `numeric`, `formatting`, `parsing`
- [devlake](pkg/devlake) - Client for the DevLake Webhook API. `webhook`, `api client`
- [dnscache](pkg/dnscache) - DNS resolution with caching support. `dns`, `caching`, `networking`
- [encode](pkg/encode) - Utilities for data encoding and serialization. `encoding`, `serialization`
- [encrypt](pkg/encrypt) - Helpers for encryption and decryption. `encryption`, `security`
- [enumbitmap](pkg/enumbitmap) - Encode and decode slices of enumeration strings as integer bitmap values. `enum`, `bitmap`, `encoding`
- [enumcache](pkg/enumcache) - Caching for enumeration values with bitmap support. `enum`, `caching`
- [enumdb](pkg/enumdb) - Helpers for storing and retrieving enumeration sets in databases. `enum`, `database`
- [errutil](pkg/errutil) - Error utility functions, including error tracing. `error handling`, `utilities`
- [filter](pkg/filter) - Generic rule-based filtering for in-memory slices (of structs, scalars, or any). `filtering`, `collections`
- [healthcheck](pkg/healthcheck) - Health check endpoints and logic. `health`, `monitoring`
- [httpclient](pkg/httpclient) - HTTP client with enhanced features. `http`, `client`
- [httpretrier](pkg/httpretrier) - HTTP request retry logic. `http`, `retry`
- [httpreverseproxy](pkg/httpreverseproxy) - HTTP reverse proxy implementation. `http`, `reverse proxy`
- [httpserver](pkg/httpserver) - HTTP server setup and management. `http`, `server`
- [httputil](pkg/httputil) - HTTP utility functions. `http`, `utilities`
- [jsendx](pkg/httputil/jsendx) - Helpers for JSend-compliant responses. `http`, `response formatting`
- [ipify](pkg/ipify) - IP address lookup using the ipify service. `ip lookup`, `networking`, `external service`
- [jirasrv](pkg/jirasrv) - Client for Jira server APIs. `api client`, `integration`
- [jwt](pkg/jwt) - JSON Web Token creation and validation. `jwt`, `authentication`, `security`
- [kafka](pkg/kafka) - Kafka producer and consumer utilities. `kafka`, `messaging`
- [logsrv](pkg/logsrv) - Default slog logger with zerolog handler. `logging`, `slog`, `zerolog`
- [logutil](pkg/logutil) - General log utilities for log/slog integration. `logging`, `utilities`
- [maputil](pkg/maputil) - Helpers for Go map manipulation. `map utilities`, `collections`
- [metrics](pkg/metrics) - Metrics collection and reporting. `metrics`, `monitoring`
- [opentel](pkg/metrics/opentel) - OpenTelemetry metrics exporter (includes tracing). `opentelemetry`, `metrics`, `tracing`
- [prometheus](pkg/metrics/prometheus) - Prometheus metrics exporter. `prometheus`, `metrics`
- [statsd](pkg/metrics/statsd) - StatsD metrics exporter. `statsd`, `metrics`
- [mysqllock](pkg/mysqllock) - Distributed locking using MySQL. `mysql`, `locking`, `distributed`
- [numtrie](pkg/numtrie) - Trie data structure for numeric keys with partial matching. `data structure`, `trie`
- [paging](pkg/paging) - Helpers for data pagination. `pagination`, `utilities`
- [passwordhash](pkg/passwordhash) - Password hashing and verification. `password hashing`, `security`, `argon2id`, `PHC`
- [passwordpwned](pkg/passwordpwned) - Password breach checking via HaveIBeenPwned. `password breach`, `security`
- [periodic](pkg/periodic) - Periodic task scheduling. `scheduling`, `tasks`
- [phonekeypad](pkg/phonekeypad) - Phone keypad mapping utilities. `phone`, `mapping`, `utilities`
- [profiling](pkg/profiling) - Application profiling tools. `profiling`, `performance`
- [random](pkg/random) - Utilities for random data generation, including UUID. `random`, `utilities`
- [redact](pkg/redact) - Fast single-pass redaction of secrets (headers, JSON, form data, DSNs, JWTs, PEM keys, card numbers) in logs and HTTP dumps. `redaction`, `privacy`
- [redis](pkg/redis) - Redis client and utilities. `redis`, `database`, `caching`
- [retrier](pkg/retrier) - Retry logic for operations. `retry`, `utilities`
- [s3](pkg/s3) - Helpers for AWS S3 integration. `aws`, `s3`
- [sfcache](pkg/sfcache) - Simple in-memory, thread-safe, fixed-size, single-flight cache for expensive lookups. `caching`, `thread-safe`, `single-flight`
- [slack](pkg/slack) - Client for sending messages via the Slack API Webhook. `slack`, `webhook`, `messaging`
- [sleuth](pkg/sleuth) - Client for the Sleuth.io API. `api client`, `integration`
- [sliceutil](pkg/sliceutil) - Utilities for slice manipulation. `slice utilities`, `collections`
- [sqlconn](pkg/sqlconn) - Helpers for SQL database connections. `sql`, `database`
- [sqltransaction](pkg/sqltransaction) - SQL transaction management. `sql`, `transactions`
- [sqlutil](pkg/sqlutil) - SQL utility functions. `sql`, `utilities`
- [sqlxtransaction](pkg/sqlxtransaction) - Helpers for SQLX transactions. `sqlx`, `transactions`
- [sqs](pkg/sqs) - Utilities for AWS SQS (Simple Queue Service) integration. `aws`, `sqs`, `messaging`
- [stringkey](pkg/stringkey) - Create unique hash keys from multiple strings. `string keys`, `hashing`
- [stringmetric](pkg/stringmetric) - String similarity and distance metrics. `text similarity`, `metrics`
- [strsplit](pkg/strsplit) - Utilities to split strings and Unicode text. `string utilities`, `text`
- [testutil](pkg/testutil) - Utilities for testing. `testing`, `utilities`
- [threadsafe](pkg/threadsafe) - Thread-safe data structures. `thread-safe`, `concurrency`
- [tsmap](pkg/threadsafe/tsmap) - Thread-safe map implementation. `thread-safe`, `map`
- [tsslice](pkg/threadsafe/tsslice) - Thread-safe slice implementation. `thread-safe`, `slice`
- [timeutil](pkg/timeutil) - Time and date utilities. `time`, `date utilities`
- [traceid](pkg/traceid) - Trace ID propagation and context management. `tracing`, `ids`
- [typeutil](pkg/typeutil) - Type conversion and utility functions. `type conversion`, `utilities`
- [uhex](pkg/uhex) - Fixed-width, lowercase hexadecimal encoders for unsigned integers and byte arrays. `hex`, `encoding`, `utilities`
- [validator](pkg/validator) - Data validation utilities. `validation`, `utilities`
- [valkey](pkg/valkey) - Wrapper client for interacting with valkey.io, an open-source in-memory data store. `data store`, `client`

## Developers Quick Start

Requirements:

- Go (latest stable; repository is configured for Go 1.27)
- Python 3 (required for additional tests)

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

# DEVMODE=LOCAL make version format clean mod deps generate qa example

# 1. make version       : Update this library version in the examples
# 2. make format        : Format the source code
# 3. make clean         : Remove any build artifact
# 4. make mod           : Download dependencies
# 5. make deps          : Get dependencies
# 6. make generate      : Generate Go code automatically (test mocks)
# 7. make qa            : Run all tests and static analysis tools
    # 7.1. make linter      : Check the code with multiple linters (golangci/golangci-lint)
    # 7.2. make test        : Run unit tests (go test)
    # 7.3. make coverage    : Generate the coverage report (/target/report/coverage.html)
# 8. make example       : Build and test the service example
    # 8.1. make clean       : Remove any build artifact
    # 8.2. make mod         : Download dependencies
    # 8.3. make deps        : Get dependencies
    # 8.4. make gendoc      : Generate static documentation from /doc/src (gomplate)
    # 8.5. make generate    : Generate Go code automatically (test mocks)
    # 8.6. make qa          : Run all tests and static analysis tools
        # 8.6.1. make linter    : Check the code with multiple linters (golangci/golangci-lint)
        # 8.6.2. make confcheck : Check the configuration files (jv)
        # 8.6.3. make test      : Run unit tests (go test)
        # 8.6.4. make coverage  : Generate the coverage report (target/report/coverage.html)
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

Contributions are welcome. Please review [CONTRIBUTING.md](https://github.com/tecnickcom/nurago/blob/main/CONTRIBUTING.md) before opening a pull request.
