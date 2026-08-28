# MAKEFILE
#
# @author      Nicola Asuni
# @link        https://github.com/tecnickcom/nurago
# ------------------------------------------------------------------------------

SHELL=/bin/bash
.SHELLFLAGS=-o pipefail -c

# Project owner
OWNER=tecnickcom

# Project vendor
VENDOR=${OWNER}

# Lowercase VENDOR name for Docker
LCVENDOR=$(shell echo "${VENDOR}" | tr '[:upper:]' '[:lower:]')

# CVS path (path to the parent dir containing the project)
CVSPATH=github.com/${VENDOR}

# Project name
PROJECT=nurago

# Project version
VERSION=$(shell cat VERSION)

# Project release number (packaging build number)
RELEASE=$(shell cat RELEASE)

# Current directory
CURRENTDIR=$(CURDIR)/

# Target directory
TARGETDIR=target

# Directory where to store binary utility tools
BINUTIL=$(TARGETDIR)/binutil

# GO lang path
ifeq ($(GOPATH),)
	# extract the GOPATH
	GOPATH=$(shell echo "$(CURDIR)" | sed 's|/src/.*||')
endif

# Add the GO binary dir in the PATH
export PATH := $(GOPATH)/bin:$(PATH)

# sed argument for in-place substitutions
SEDINPLACE=-i
ifeq ($(shell uname -s),Darwin)
	SEDINPLACE=-i ''
endif

# Docker tag
DOCKERTAG=$(VERSION)-$(RELEASE)

# Docker command
ifeq ($(DOCKER),)
	DOCKER=$(shell which docker)
endif

# Common commands
GO=GOPATH="$(GOPATH)" GOPRIVATE=$(CVSPATH) $(shell which go)
GOVERSION=${shell go version | grep -Eo '(go[0-9]+.[0-9]+)'}
GOFMT=$(shell which gofmt)
GOTEST=$(GO) test
GODOC=GOPATH="$(GOPATH)" $(shell which godoc)
GOLANGCILINT=$(BINUTIL)/golangci-lint
GOLANGCILINTVERSION=v2.13.2
GOVULNCHECK=$(GO) tool govulncheck
BENCHSTAT=$(GO) tool benchstat

# Benchmark flags for deterministic allocation stats (fixed iteration count, repeated for statistical significance)
BENCHFLAGS=-tags=unit,benchmark -run=^$$ -bench=. -benchmem -benchtime=100x -count=6

# Git reference to compare benchmark allocations against
ifeq ($(BENCHBASE),)
	BENCHBASE=main
endif

# Directory containing the source code
SRCDIR=./pkg

# List of packages
GOPKGS=$(shell $(GO) list $(SRCDIR)/...)

# Enable junit report when not in LOCAL mode
ifeq ($(strip $(DEVMODE)),LOCAL)
	TESTEXTRACMD=&& $(GO) tool cover -func=$(TARGETDIR)/report/coverage.out
else
	TESTEXTRACMD=2>&1 | tee >(PATH="$(GOPATH)/bin:$(PATH)" go-junit-report > $(TARGETDIR)/test/report.xml); test $${PIPESTATUS[0]} -eq 0
endif

# Set default configuration file to generate a new project from the example service
ifeq ($(CONFIG),)
	CONFIG=project.cfg
endif

# Include the configuration file
include $(CONFIG)

# --- MAKE TARGETS ---

.PHONY: help
help:
	@echo ""
	@echo "$(PROJECT) Makefile."
	@echo "GOPATH=$(GOPATH)"
	@echo "The following commands are available:"
	@echo ""
	@awk '/^## /{desc=substr($$0,4)} /^\.PHONY:/{if(NF>1) {target=$$2; if(desc) printf "  make %-15s: %s\n",target,desc; desc=""}}' Makefile
	@echo ""
	@echo "To test and build everything from scratch, use the shortcut:"
	@echo "    make x"
	@echo ""

# Alias for help target
all: help

## Test and build everything from scratch
.PHONY: x
x:
	DEVMODE=LOCAL $(MAKE) version format clean mod deps generate qa example

## Remove any build artifact
.PHONY: clean
clean:
	rm -rf "$(TARGETDIR)"

## Generate the coverage report
.PHONY: coverage
coverage: ensuretarget
	$(GO) tool cover -html="$(TARGETDIR)/report/coverage.out" -o "$(TARGETDIR)/report/coverage.html"

## Build everything inside a Docker container
.PHONY: dbuild
dbuild: dockerdev
	@mkdir -p "$(TARGETDIR)"
	@rm -rf "$(TARGETDIR)/"*
	@echo 0 > "$(TARGETDIR)/make.exit"
	CVSPATH=$(CVSPATH) VENDOR=$(LCVENDOR) PROJECT=$(PROJECT) MAKETARGET='$(MAKETARGET)' DOCKERTAG='$(DOCKERTAG)' "$(CURRENTDIR)dockerbuild.sh"
	@exit `cat $(TARGETDIR)/make.exit`

## Get dependencies
.PHONY: deps
deps: ensuretarget
	curl --silent --show-error --fail --location "https://golangci-lint.run/install.sh" | sh -s -- -b "$(BINUTIL)" $(GOLANGCILINTVERSION)

## Build a base development Docker image
.PHONY: dockerdev
dockerdev:
	$(DOCKER) build --pull --tag "${LCVENDOR}/dev_${PROJECT}" --file ./resources/docker/Dockerfile.dev ./resources/docker/

## Create the target directories if missing
.PHONY: ensuretarget
ensuretarget:
	@mkdir -p "$(TARGETDIR)/test"
	@mkdir -p "$(TARGETDIR)/report"
	@mkdir -p "$(TARGETDIR)/binutil"

## Build and test the service example
.PHONY: example
example:
	cd examples/service && \
	$(MAKE) clean mod deps gendoc generate qa build

## Format the source code
.PHONY: format
format:
	@find "$(SRCDIR)" -type f -name "*.go" -exec $(GOFMT) -s -w {} \;
	cd examples/service && $(MAKE) format

## Generate go code automatically
.PHONY: generate
generate:
	@find "$(SRCDIR)" -type f -name "*mock_test.go" -exec rm {} \;
	$(GO) generate $(GOPKGS)

## Check dependencies for known vulnerabilities
.PHONY: govulncheck
govulncheck:
	@echo -e "\n\n>>> START: Vulnerability check <<<\n\n"
	$(GOVULNCHECK) "$(SRCDIR)/..."
	@echo -e "\n\n>>> END: Vulnerability check <<<\n\n"

## Check code against multiple linters
.PHONY: linter
linter:
	@echo -e "\n\n>>> START: Static code analysis <<<\n\n"
	$(GOLANGCILINT) run --max-issues-per-linter 0 --max-same-issues 0 "$(SRCDIR)/..."
	@echo -e "\n\n>>> END: Static code analysis <<<\n\n"

## Download dependencies
.PHONY: mod
mod: gotools
	$(GO) mod download all

## Generate a new project from the example using the data set via CONFIG=project.cfg
.PHONY: project
project:
	cd examples/service && $(MAKE) clean
	@mkdir -p "./target/$(nuragoexamplecvspath)/$(nuragoexample)"
	@rm -rf "./target/$(nuragoexamplecvspath)/$(nuragoexample)/"*
	@cp -rf examples/service/. "./target/$(nuragoexamplecvspath)/$(nuragoexample)/"
	sed $(SEDINPLACE) '/^replace /d' "./target/$(nuragoexamplecvspath)/$(nuragoexample)/go.mod"
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -depth -name '*nuragoexample*' -execdir sh -c 'f="{}"; mv -- "$$f" "$$(echo "$$f" | sed s/nuragoexample/$(nuragoexample)/)"' \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -depth -name '*NURAGOEXAMPLE*' -execdir sh -c 'f="{}"; mv -- "$$f" "$$(echo "$$f" | sed s/NURAGOEXAMPLE/$(NURAGOEXAMPLE)/)"' \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexampleshortdesc|$(nuragoexampleshortdesc)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexamplelongdesc|$(nuragoexamplelongdesc)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexampleauthor|$(nuragoexampleauthor)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexampleemail|$(nuragoexampleemail)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexamplecvspath|$(nuragoexamplecvspath)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexampleprojectlink|$(nuragoexampleprojectlink)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexampleowner|$(nuragoexampleowner)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexamplevcsgit|$(nuragoexamplevcsgit)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|nuragoexample|$(nuragoexample)|g" {} \;
	find "./target/$(nuragoexamplecvspath)/$(nuragoexample)" -type f -exec sed $(SEDINPLACE) "s|NURAGOEXAMPLE|$(NURAGOEXAMPLE)|g" {} \;

## Run all tests and static analysis tools
.PHONY: qa
qa: linter govulncheck test coverage

## Tag the Git repository
.PHONY: tag
tag:
	git tag -a "v$(VERSION)" -m "Version $(VERSION)" && \
	git push origin --tags

## Run unit tests
.PHONY: test
test: ensuretarget
	@echo -e "\n\n>>> START: Unit Tests <<<\n\n"
	$(GOTEST) \
	-shuffle=on \
	-tags=unit,benchmark \
	-covermode=atomic \
	-bench=. \
	-benchtime=1x \
	-race \
	-failfast \
	-coverprofile=$(TARGETDIR)/report/coverage.out \
	-v $(GOPKGS) $(TESTEXTRACMD)
	@echo -e "\n\n>>> END: Unit Tests <<<\n\n"

## Run benchmarks (without -race or coverage) and store the results for comparison (target/report/bench.txt)
.PHONY: bench
bench: ensuretarget
	@echo -e "\n\n>>> START: Benchmarks <<<\n\n"
	$(GOTEST) $(BENCHFLAGS) $(GOPKGS) | tee "$(TARGETDIR)/report/bench.txt"
	@echo -e "\n\n>>> END: Benchmarks <<<\n\n"

## Run base benchmarks (without -race or coverage) and store the results for comparison (target/report/bench_base.txt)
.PHONY: benchbase
benchbase:
	@echo -e "\n\n>>> START: Base Benchmarks <<<\n\n"
	@rm -rf "$(TARGETDIR)/benchbase"
	@git worktree prune
	git worktree add --detach "$(TARGETDIR)/benchbase" "$(BENCHBASE)"
	cd "$(TARGETDIR)/benchbase" && $(GOTEST) $(BENCHFLAGS) ./pkg/... | tee "$(TARGETDIR)/report/bench_base.txt"
	git worktree remove --force "$(TARGETDIR)/benchbase"
	@echo -e "\n\n>>> END: Base Benchmarks <<<\n\n"

## Compare benchmark allocation counts against a base git ref and fail on regressions (set BENCHBASE=ref, default main)
.PHONY: benchcmp
benchcmp: bench benchbase
	$(MAKE) benchgate BASELINE="$(TARGETDIR)/report/bench_base.txt"

## Compare benchmark allocation counts against a baseline file and fail on regressions (set BASELINE=path/to/baseline.txt)
.PHONY: benchgate
benchgate:
	@echo -e "\n\n>>> START: Benchmark allocation gate <<<\n\n"
	@test -f "$(BASELINE)" || { echo "ERROR: baseline file not found: $(BASELINE)"; exit 1; }
	@test -f "$(TARGETDIR)/report/bench.txt" || { echo "ERROR: benchmark results not found, run 'make bench' first"; exit 1; }
	$(BENCHSTAT) $(BASELINE) "$(TARGETDIR)/report/bench.txt" 2> >(grep -vE "^[A-Z][0-9]+: " >&2)
	$(BENCHSTAT) -filter ".unit:allocs/op" -format csv $(BASELINE) "$(TARGETDIR)/report/bench.txt" 2> >(grep -vE "^[A-Z][0-9]+: " >&2) \
	| awk -F, '\
	$$1!="" && $$1!="geomean" && $$7 ~ /^p=/ && $$6!="~" && ($$4+0) > ($$2+0) \
	{print "ALLOC REGRESSION: "$$1" allocs/op: "$$2" -> "$$4" ("$$6")"; fail=1} END {exit fail}'
	@echo -e "\n\n>>> END: Benchmark allocation gate <<<\n\n"

## Get the go tools
.PHONY: gotools
gotools:
	$(GO) get -tool go.uber.org/mock/mockgen@latest
	$(GO) get -tool golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) get -tool golang.org/x/perf/cmd/benchstat@latest
	$(GO) install github.com/jstemmer/go-junit-report/v2@latest

## Update everything
.PHONY: updateall
updateall: updatego updatelint updatemod

## Update Go version
.PHONY: updatego
updatego:
	$(eval LAST_GO_TOOLCHAIN=$(shell curl -s https://go.dev/dl/ | grep -oE 'go[0-9]+\.[0-9]+\.[0-9]+\.linux-amd64\.tar\.gz' | head -n 1 | grep -oE 'go[0-9]+\.[0-9]+\.[0-9]+'))
	$(eval LAST_GO_VERSION=$(shell echo ${LAST_GO_TOOLCHAIN} | grep -oE '[0-9]+\.[0-9]+'))
	sed $(SEDINPLACE) "s|^go [0-9]*\.[0-9]*.*$$|go ${LAST_GO_VERSION}|g" go.mod
	sed $(SEDINPLACE) "s|^toolchain go[0-9]*\.[0-9]*\.[0-9]*$$|toolchain ${LAST_GO_TOOLCHAIN}|g" go.mod
	cd examples/service && $(MAKE) updatego

## Update golangci-lint version
.PHONY: updatelint
updatelint:
	$(eval LAST_GOLANGCILINT_VERSION=$(shell curl -sL https://github.com/golangci/golangci-lint/releases/latest | sed -n 's/.*<title>Release \(v[0-9]*\.[0-9]*\.[0-9]*\).*/\1/p'))
	sed $(SEDINPLACE) "s|^GOLANGCILINTVERSION=v[0-9]*\.[0-9]*\.[0-9]*$$|GOLANGCILINTVERSION=${LAST_GOLANGCILINT_VERSION}|g" Makefile
	cd examples/service && $(MAKE) updatelint

## Update dependencies
.PHONY: updatemod
updatemod: mod
	$(GO) get -t -u ./... && \
	$(GO) mod tidy -compat=$(shell sed -n -E 's/^go ([0-9]+\.[0-9]+).*/\1/p' go.mod)
	cd examples/service && $(MAKE) updatemod

## Update this library version in the examples
.PHONY: version
version:
	sed $(SEDINPLACE) "s|github.com/tecnickcom/nurago v.*$$|github.com/tecnickcom/nurago v$(VERSION)|" examples/service/go.mod

## Increase the patch number in the VERSION file
.PHONY: versionup
versionup:
	echo ${VERSION} | awk -F. '{printf("%d.%d.%d\n",$$1,$$2,(($$3+1)));}' > VERSION
	$(MAKE) version
