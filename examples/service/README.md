# nuragoexample

*nuragoexampleshortdesc*

![nuragoexample logo](doc/images/logo.png)

* **category:**    Application
* **copyright:**   2025-2026 nuragoexampleowner
* **license:**     [LICENSE](https://github.com/nuragoexampleowner/nuragoexample/blob/main/LICENSE)
* **cvs:**         https://github.com/nuragoexampleowner/nuragoexample

[![check](https://github.com/nuragoexampleowner/nuragoexample/actions/workflows/check.yaml/badge.svg)](https://github.com/nuragoexampleowner/nuragoexample/actions/workflows/check.yaml)

----------

## TOC

* [Description](#description)
* [Documentation](#documentation)
  * [public](#documentation_public)
    * [General](#documentation_public_general)
* [Development](#development)
* [Deployment](#deployment)

----------

<a name="description"></a>

## Description

nuragoexamplelongdesc


----------



<a name="documentation"></a>

## Documentation

<a name="documentation_public"></a>

* public
  <a name="documentation_public_general"></a>
  * General  
    _General project documentation_
    * [GitHub project page](nuragoexampleprojectlink)


----------



<a name="development"></a>

## Development

* [Style and Conventions](#style)
* [Quick Start](#quickstart)
* [Project Structure](#structure)
* [Running all tests](#runtest)
* [Documentation](#gendoc)
* [Usage](#usage)
* [Configuration](CONFIG.md)
* [Examples](#examples)
* [Logs](#logs)
* [Metrics](#metrics)
* [Profiling](#profiling)
* [OpenAPI](#openapi)
* [Docker](#docker)

<a name="style"></a>

## Style and Conventions

For the general style and conventions, please refer to [external documents](https://github.com/uber-go/guide/blob/master/style.md).

<a name="quickstart"></a>

## Developers' Quick Start

To quickly get started with this project, follow these steps:

1. Ensure you have installed the latest Go version and Python3 for some extra tests.
2. Clone the repository: `git clone https://github.com/nuragoexampleowner/nuragoexample.git`.
3. Change into the project directory: `cd nuragoexample`.
4. Install the required dependencies and test everything: `DEVMODE=LOCAL make x`.

Now you are ready to start developing with nuragoexample!

This project includes a *Makefile* that allows you to test and build the project in a Linux-compatible system with simple commands.  
All the artifacts and reports produced using this *Makefile* are stored in the *target* folder.  

Alternatively, everything can be built inside a [Docker](https://www.docker.com) container using the command `make dbuild` that uses the environment defined at `resources/docker/Dockerfile.dev`.

To see all available options:

```bash
make help
```

<a name="structure"></a>

## Project Structure

The `examples/service` project is organized to keep runtime wiring, HTTP exposure, operational assets, and generated output clearly separated.
That structure matters because it lets you extend the example without mixing application code, deployment assets, and generated artifacts in the same place.

## Top Features

* **Three-server runtime layout** via monitoring, private, and public HTTP endpoints.
	Why it matters: it shows how to isolate operational traffic from internal and external APIs from day one.
* **Configuration-first bootstrap** in `internal/cli`.
	Why it matters: startup, logging, metrics, health checks, and graceful shutdown are all wired in one place, which keeps new features easier to add safely.
* **Operational assets shipped with the codebase** in `resources`.
	Why it matters: local development, integration tests, packaging, container builds, and service-manager integration stay reproducible.
* **Generated documentation and artifacts kept separate** in `doc` and `target`.
	Why it matters: developers can regenerate docs and build outputs without polluting source packages.

## Root Folders

| Folder | Function |
| --- | --- |
| `cmd` | Application entry point. Holds `main.go`, the executable startup flow, and package-level service documentation. |
| `doc` | Project documentation sources and generated documentation assets. |
| `internal` | Private application packages that wire configuration, handlers, metrics, and optional dependencies. |
| `resources` | Runtime, packaging, database, dashboard, and test fixture assets used by developers, CI, and deployment workflows. |
| `target` | Generated build outputs, reports, temporary test artifacts, and packaged files. |

## Key Root-Level Files

| Path | Function |
| --- | --- |
| `go.mod` | Defines the module and the service-specific dependency graph. |
| `Makefile` | Main developer workflow entry point for build, test, documentation, and packaging commands. |
| `docker-compose-local.yml` | Local development stack definition. |
| `docker-compose-int.yml` | Integration-test stack definition. |
| `dockerbuild.sh` | Helper script for Docker-oriented build flows. |
| `openapi_public.yaml` | Public API contract for the externally exposed endpoints. |
| `openapi_private.yaml` | Private API contract for internal endpoints. |
| `openapi_monitoring.yaml` | Monitoring API contract for operational routes. |
| `schemathesis.toml` | Check selection for the contract tests run against the OpenAPI specs. |
| `test.integration.Dockerfile` | Container image definition used by integration scenarios. |
| `README.md` | Generated top-level project guide for developers and operators. |

## Folder Breakdown

### `cmd`

This folder contains the executable boundary of the service.

| Path | Function |
| --- | --- |
| `cmd/main.go` | Starts the program, builds the CLI, and handles process-level exit behavior. |
| `cmd/doc.go` | High-level package documentation describing runtime topology and developer-facing features. |

### `doc`

This folder keeps the service documentation maintainable by separating templates from generated output.

| Path | Function |
| --- | --- |
| `doc/src` | Source templates, config metadata, and schemas used to generate Markdown documentation. |
| `doc/images` | Images referenced by the generated docs, such as the project logo. |
| `doc/CONFIG.md` | Generated configuration reference for runtime settings. |

### `internal`

This folder contains the application-specific implementation details that should not be imported by external modules.

| Folder | Function |
| --- | --- |
| `internal/cli` | Loads configuration, defines CLI flags, builds the dependency graph, and starts the service lifecycle. |
| `internal/db` | Shared database abstractions used by the service wiring. |
| `internal/httphandlerpriv` | Private API route definitions and handlers. |
| `internal/httphandlerpub` | Public API route definitions and handlers. |
| `internal/item` | Example business domain: domain types, service rules, and the SQL repository behind the item endpoints. |
| `internal/metrics` | Service-specific Prometheus collectors and instrumentation helpers layered on top of nurago metrics. |

### `resources`

This folder is the operational companion to the Go code.

| Folder | Function |
| --- | --- |
| `resources/db` | Database bootstrap assets, users, and versioned migrations for MySQL and PostgreSQL. |
| `resources/debian` | Debian packaging metadata and lifecycle scripts. |
| `resources/docker` | Dockerfiles for development and runtime container images. |
| `resources/etc` | Default configuration files, schemas, certificates layout, and init/systemd service definitions. |
| `resources/grafana` | Ready-to-import dashboard definitions for observability. |
| `resources/rpm` | RPM packaging specification and install layout. |
| `resources/test` | Deterministic configs and fixtures for local, integration, and API-test scenarios. |
| `resources/usr` | Files staged for installation into standard Unix package paths. |

### `target`

This folder is generated and is intended to be disposable.

| Folder | Function |
| --- | --- |
| `target/binutil` | Helper binaries downloaded or built for developer and CI workflows. |
| `target/report` | Generated reports such as test and coverage output. |
| `target/test` | Temporary files produced during test execution. |
| `target/usr` | Built install tree used for packaging and image assembly. |

## Benefits Summary

* Faster onboarding because each concern has an obvious home.
* Safer feature work because bootstrap, handlers, and operational assets are separated cleanly.
* Better operability because monitoring, packaging, and test resources are part of the project instead of tribal knowledge.
* Easier extension because new routes, dependencies, and deployment assets fit into an existing structure instead of forcing a reorganization later.


<a name="runtest"></a>

## Running all tests

Before committing the code, please check if it passes all tests using

```bash
make x
```

<a name="gendoc"></a>

## Documentation

The `README.md` documentation file is generated using the source templates in `doc/src` via the `make gendoc` command.

To update links and common information edit the file `doc/src/config.yaml` in YAML format.
The schema of the configuration file is defined by the JSON schema: `doc/src/config.schema.json`.
The document templates are defined by the `*.tmpl` files in [gomplate](https://docs.gomplate.ca)-compatible format.

To regenerate the static documentation file:

```bash
make gendoc
```

<a name="usage"></a>

## Usage

```bash
nuragoexample [flags]

Flags:

-c, --configDir  string  Configuration directory to be added on top of the search list
-f, --logFormat  string  Logging format: CONSOLE, JSON
-o, --logLevel   string  Log level: EMERGENCY, ALERT, CRITICAL, ERROR, WARNING, NOTICE, INFO, DEBUG
```

<a name="examples"></a>

## Examples

Once the application has been compiled with `make build`, it can be quickly tested:

```bash
target/usr/bin/nuragoexample -c resources/test/etc/nuragoexample
```

<a name="logs"></a>

## Logs

This program logs the log messages in JSON format:

```json
{
  "level": "info",
  "timestamp": 1595942715776382171,
  "msg": "Request",
  "program": "nuragoexample",
  "version": "0.0.0",
  "release": "0",
  "hostname":"myserver",
  "request_id": "c4iah65ldoyw3hqec1rluoj93",
  "request_method": "GET",
  "request_path": "/uid",
  "request_query": "",
  "request_uri": "/uid",
  "request_useragent": "curl/7.69.1",
  "remote_ip": "[::1]:36790",
  "response_code": 200,
  "response_message": "OK",
  "response_status": "success",
  "response_data": "avxkjeyk43av"
}
```

Logs are sent to stderr by default.

The log level can be set either in the configuration or as command argument (`logLevel`).

<a name="metrics"></a>

## Metrics

This service provides [Prometheus](https://prometheus.io/) metrics at the `/metrics` endpoint.

<a name="profiling"></a>

## Profiling

This service provides [PPROF](https://github.com/google/pprof) profiling data at the `/pprof` endpoint.

The pprof data can be analyzed and displayed using the pprof tool:

```bash
go install github.com/google/pprof@latest
```

Example:

```bash
pprof -seconds 10 -http=localhost:8182 http://INSTANCE_URL:PORT/pprof/profile
```

<a name="openapi"></a>

## OpenAPI

The nuragoexample API is specified via the [OpenAPI 3](https://www.openapis.org/) files: `openapi_public.yaml`, `openapi_private.yaml` and `openapi_monitoring.yaml`.

The OpenAPI files can be edited using the Swagger Editor:

```bash
docker pull swaggerapi/swagger-editor
docker run -p 8056:8080 swaggerapi/swagger-editor
```

and pointing the Web browser to http://localhost:8056

<a name="docker"></a>

## Docker

To build a Docker scratch container for the nuragoexample executable binary execute the following command:

```bash
make docker
```

### Useful Docker commands

To manually create the container you can execute:

```bash
docker build --tag="nuragoexampleowner/nuragoexampledev" .
```

To log into the newly created container:

```bash
docker run -t -i nuragoexampleowner/nuragoexampledev /bin/bash
```

To get the container ID:

```bash
CONTAINER_ID=`docker ps -a | grep nuragoexampleowner/nuragoexampledev | cut -c1-12`
```

To delete the newly created docker container:

```bash
docker rm -f $CONTAINER_ID
```

To delete the docker image:

```bash
docker rmi -f nuragoexampleowner/nuragoexampledev
```


----------

<a name="deployment"></a>

## Deployment

### Deployment in Production

Add here information on how to deploy in production.


----------


