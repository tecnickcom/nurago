# Resources Directory Guide

This directory contains all runtime, packaging, database, and test assets for the example service.

If the Go code is the application logic, this folder is the operational contract around it: how it is configured, containerized, deployed, observed, and validated.

## What Problem This Folder Solves

Building a service is only half the work. Teams also need repeatable assets for:

- database setup and migrations,
- local and integration execution,
- OS/service manager integration,
- Debian and RPM packaging,
- observability dashboards,
- deterministic test fixtures.

This folder centralizes those concerns so developers and CI/CD pipelines use the same source of truth.

## Top Features (and Why They Matter)

- Multi-database support (MySQL and PostgreSQL)
	- Enables environment parity and easier migration between data stores.
- Flyway-ready migration layout
	- Makes schema changes versioned, auditable, and automatable.
- End-to-end test fixtures for local and integration modes
	- Reduces flaky tests and shortens feedback loops.
- Dual service boot support (systemd + SysV init)
	- Improves portability across modern and legacy Linux distributions.
- Packaging templates for both Debian and RPM ecosystems
	- Speeds up release engineering and standardizes installation behavior.
- Grafana dashboard template
	- Gives developers and operators immediate visibility into service health and performance.

## Directory Overview

| Folder | Problem solved | Key files |
|---|---|---|
| db | Provision database objects and versioned schema/data for integration environments. | db.dockerfile, mysql/*, postgres/* |
| debian | Define Debian package metadata and lifecycle hooks. | control, rules, postinst, postrm |
| docker | Build environments for development/testing and minimal runtime images. | Dockerfile.dev, Dockerfile.run |
| etc | Ship runtime configuration, validation schema, and service manager definitions. | nuragoexample/config.json, config.schema.json, systemd unit |
| grafana | Provide a ready-to-import monitoring dashboard. | dashboard.json |
| rpm | Define RPM package structure and installed assets. | rpm.spec |
| test | Keep deterministic configs and integration fixtures for local/CI test runs. | test/etc/*, test/int/*, test/local/* |

## File-by-File Reference

### db

#### db/db.dockerfile

- Problem solved: wraps SQL migration scripts in a Flyway image so DB bootstrapping can run consistently in containers.
- Why it matters: DB initialization is reproducible across CI, integration, and local environments.

#### db/mysql/create.sql

- Problem solved: creates and selects the MySQL database used by the example service.
- Why it matters: eliminates manual DB creation steps.

#### db/mysql/schema/V0000__schema.sql

- Problem solved: creates the base MySQL schema objects (item table).
- Why it matters: establishes a versioned baseline migration.

#### db/mysql/int/V1001__example_table.sql

- Problem solved: seeds example MySQL items for integration-style scenarios.
- Why it matters: tests have predictable records to query.

#### db/mysql/users.sql

- Problem solved: creates MySQL read-write and read-only users and grants permissions.
- Why it matters: validates least-privilege connection patterns used by the service.

#### db/postgres/schema/V0000__schema.sql

- Problem solved: creates PostgreSQL roles/permissions and baseline table schema.
- Why it matters: mirrors production-like role separation for safer access control testing.

#### db/postgres/int/V1001_example_table.sql

- Problem solved: seeds PostgreSQL sample data for tests.
- Why it matters: keeps integration checks stable and repeatable.

#### db/postgres/users.sql

- Problem solved: idempotently creates PostgreSQL roles/users and memberships.
- Why it matters: allows repeated setup runs without failing due to existing principals.

### debian

#### debian/changelog

- Problem solved: records package release metadata for Debian tooling.
- Why it matters: required by Debian packaging workflows and useful for traceability.

#### debian/compat

- Problem solved: pins debhelper compatibility level.
- Why it matters: ensures predictable behavior across Debian build tools.

#### debian/control

- Problem solved: defines package metadata, dependencies, and description.
- Why it matters: controls how package managers interpret and install the service.

#### debian/copyright

- Problem solved: declares licensing and ownership metadata for packaged artifacts.
- Why it matters: required for compliant distribution.

#### debian/postinst

- Problem solved: post-install hook that registers service startup defaults.
- Why it matters: makes package install operational immediately.

#### debian/postrm

- Problem solved: post-removal hook that unregisters service startup entries.
- Why it matters: leaves host init configuration clean after uninstall.

#### debian/rules

- Problem solved: Debian package build script entrypoint.
- Why it matters: central place to drive package build steps.

#### debian/source/format

- Problem solved: declares source package format (3.0 quilt).
- Why it matters: ensures Debian tooling handles source packages correctly.

### docker

#### docker/Dockerfile.dev

- Problem solved: creates a development/test image with packaging and test tools.
- Why it matters: standardizes toolchain versions used by contributors and CI jobs.

#### docker/Dockerfile.run

- Problem solved: produces a minimal runtime image from prebuilt artifacts.
- Why it matters: smaller surface area, faster startup, and cleaner production deployments.

### etc

#### etc/nuragoexample/config.json

- Problem solved: default runtime configuration template (clients, DB pools, log, servers, shutdown).
- Why it matters: provides a ready starting point for environment-specific overrides.

#### etc/nuragoexample/config.schema.json

- Problem solved: JSON Schema validation and documentation for config fields.
- Why it matters: catches invalid configuration early and improves editor/tooling integration.

#### etc/init.d/nuragoexample

- Problem solved: SysV init service script for legacy init systems.
- Why it matters: keeps the service deployable on older Linux distributions.

#### etc/systemd/system/nuragoexample.service

- Problem solved: systemd unit file for service lifecycle and restart policy.
- Why it matters: modern service management with hardened runtime options.

#### etc/ssl/certs/.keep

- Problem solved: keeps the certificate directory tracked in version control.
- Why it matters: expected directory structure exists even before certificates are mounted.

### grafana

#### grafana/dashboard.json

- Problem solved: prebuilt Grafana dashboard for request latency, error codes, and process metrics.
- Why it matters: faster operational visibility without building dashboards from scratch.

### rpm

#### rpm/rpm.spec

- Problem solved: defines RPM package build/install metadata and installed files.
- Why it matters: standard RPM packaging for Red Hat ecosystem distributions.

### test

#### test/etc/nuragoexample/config.json

- Problem solved: generic test config fixture with test-safe endpoints/timeouts.
- Why it matters: baseline configuration for non-production test executions.

#### test/etc/invalid/config.json

- Problem solved: intentionally invalid config fixture.
- Why it matters: verifies config validation and error handling paths.

#### test/int/entrypoint.sh

- Problem solved: orchestrates integration test readiness checks, mock setup, and test execution.
- Why it matters: reduces race conditions and makes integration runs deterministic.

#### test/int/nuragoexample/config.json

- Problem solved: integration config targeting containerized MySQL + mocked external APIs.
- Why it matters: reliable end-to-end tests in CI-like environments.

#### test/int/nuragoexample/config.json.mysql

- Problem solved: MySQL-focused integration config variant with explicit short timeouts.
- Why it matters: validates MySQL DSN and behavior under integration constraints.

#### test/int/nuragoexample/config.json.postgres

- Problem solved: PostgreSQL-focused integration config variant.
- Why it matters: validates parity of behavior across DB engines.

#### test/int/smocker/ipify_apitest.yaml

- Problem solved: defines mock response for the ipify dependency during API tests.
- Why it matters: removes external network dependency from integration runs.

#### test/local/nuragoexample/config.json

- Problem solved: local developer config defaults for running service + tests.
- Why it matters: quick local startup with known-good settings.

#### test/local/nuragoexample/config.json.mysql

- Problem solved: local MySQL-specific config variant.
- Why it matters: easy local switching between database backends.

#### test/local/nuragoexample/config.json.postgres

- Problem solved: local PostgreSQL-specific config variant.
- Why it matters: allows parity checks without editing core config files.

## Benefits Summary

- Faster onboarding: new contributors can understand operational assets quickly.
- Safer changes: schema, config, and packaging behavior are explicit and versioned.
- Better reliability: deterministic test fixtures and integration orchestration reduce flakiness.
- Deployment portability: supports containers, systemd/SysV, Debian, and RPM workflows.
- Improved observability: prebuilt Grafana assets accelerate production readiness.
