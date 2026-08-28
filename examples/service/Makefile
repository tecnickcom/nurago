# MAKEFILE
#
# @author      Nicola Asuni
# @link        https://github.com/nuragoexampleowner/nuragoexample
# ------------------------------------------------------------------------------

SHELL=/bin/bash
.SHELLFLAGS=-o pipefail -c

# Project owner
OWNER=nuragoexampleowner

# Project vendor
VENDOR=${OWNER}

# Lowercase VENDOR name for Docker
LCVENDOR=$(shell echo "${VENDOR}" | tr '[:upper:]' '[:lower:]')

# CVS path (path to the parent dir containing the project)
CVSPATH=github.com/${VENDOR}

# Project name
PROJECT=nuragoexample

# Project version
VERSION=$(shell cat VERSION)

# Project release number (packaging build number)
RELEASE=$(shell cat RELEASE)

# Name of RPM or DEB package
PKGNAME=${LCVENDOR}-${PROJECT}

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

# Path for binary files (where the executable files will be installed)
BINPATH=usr/bin/

# Path for configuration files
CONFIGPATH=etc/$(PROJECT)/

# Path for custom root CA certificates
SSLCONFIGPATH=etc/ssl/

# Search paths for system root CA certificates to include
CACERTPATH=/etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/ca-bundle.pem /etc/pki/tls/cacert.pem /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem /etc/ssl/cert.pem

# Path for init script
INITPATH=usr/lib/systemd/system/

# Path path for documentation
DOCPATH=usr/share/doc/$(PKGNAME)/

# Path path for man pages
MANPATH=usr/share/man/man1/

# Installation path for the binary files
PATHINSTBIN=$(DESTDIR)/$(BINPATH)

# Installation path for the configuration files
PATHINSTCFG=$(DESTDIR)/$(CONFIGPATH)

# Installation path for the ssl root certs
PATHINSTSSLCFG=$(DESTDIR)/$(SSLCONFIGPATH)

# Installation path for the init file
PATHINSTINIT=$(DESTDIR)/$(INITPATH)

# Installation path for documentation
PATHINSTDOC=$(DESTDIR)/$(DOCPATH)

# Installation path for man pages
PATHINSTMAN=$(DESTDIR)/$(MANPATH)

# DOCKER Packaging path (where BZ2s will be stored)
PATHDOCKERPKG=$(CURRENTDIR)target/DOCKER

# RPM Packaging path (where RPMs will be stored)
PATHRPMPKG=$(CURRENTDIR)target/RPM

# DEB Packaging path (where DEBs will be stored)
PATHDEBPKG=$(CURRENTDIR)target/DEB

# Prefix for the published docker container name
DOCKERPREFIX=

# Suffix for the published docker container name
DOCKERSUFFIX=

# Command used to check the configuration files
CONFCHECKCMD=jv resources/etc/${PROJECT}/config.schema.json

# Set default AWS region (if using AWS for deployments)
ifeq ($(AWS_DEFAULT_REGION),)
	AWS_DEFAULT_REGION=eu-west-1
endif

# STATIC is a flag to indicate whether to build using static or dynamic linking
STATIC=1
ifeq ($(STATIC),0)
	STATIC_TAG=dynamic
	STATIC_FLAG=
else
	STATIC_TAG=static
	STATIC_FLAG=-static
endif

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

# Docker compose command
ifeq ($(DOCKERCOMPOSE),)
	DOCKERCOMPOSE=docker-compose
	DOCKERCOMPOSEPLUGIN=$(DOCKER) compose
	HASDOCKERCOMPOSE := $(shell $(DOCKERCOMPOSEPLUGIN) 2> /dev/null)
	ifdef HASDOCKERCOMPOSE
		DOCKERCOMPOSE=$(DOCKERCOMPOSEPLUGIN)
	endif
endif

DOCKERCOMPOSECMD=COMPOSE_PROJECT_NAME=$(VENDOR) $(DOCKERCOMPOSE)
DOCKERBUILDARG=--build-arg HOST_USER="$(shell id -u ${USER})" --build-arg HOST_GROUP="$(shell id -g ${USER})"

# Common commands
GO=GOPATH="$(GOPATH)" GOPRIVATE=$(CVSPATH) $(shell which go)
GOVERSION=${shell go version | grep -Eo '(go[0-9]+.[0-9]+)'}
GOFMT=$(shell which gofmt)
GOTEST=$(GO) test
GODOC=GOPATH="$(GOPATH)" $(shell which godoc)
GOLANGCILINT=$(BINUTIL)/golangci-lint
GOLANGCILINTVERSION=v2.13.2
GOVULNCHECK=$(GO) tool govulncheck
DOCKERIZEVERSION=v0.9.2

# Current operating system and architecture as one string.
GOOSARCH=$(shell go env GOOS GOARCH | tr -d \\n)

# Architecture used for Docker images and helper binaries
# (matches the host architecture so containers run natively, e.g. arm64 on Apple Silicon).
DOCKERARCH=$(shell go env GOARCH)

# OS and Architecture used to build the Go binary for Docker.
LINUXGOBUILDENV=GOOS=linux GOARCH=$(DOCKERARCH)

# Environment variables for the go build command (uncomment the one that is appropriate):
#
# 32bit Raspbian:
#GOBUILDENV=env GOOS=linux GOARCH=arm GOARM=5
#
# 64bit Raspbian:
#GOBUILDENV=env GOOS=linux GOARCH=arm64
#
# Current environment
GOBUILDENV=

# Directory containing the source code
CMDDIR=./cmd
SRCDIR=./internal

# List of packages
GOPKGS=$(shell $(GO) list $(CMDDIR)/... $(SRCDIR)/...)

# Enable junit report when not in LOCAL mode
ifeq ($(strip $(DEVMODE)),LOCAL)
	TESTEXTRACMD=&& $(GO) tool cover -func=$(TARGETDIR)/report/coverage.out
else
	TESTEXTRACMD=2>&1 | tee >(PATH="$(GOPATH)/bin:$(PATH)" go-junit-report > $(TARGETDIR)/test/report.xml); test $${PIPESTATUS[0]} -eq 0
endif

# Specify api test configuration files to execute (venom YAML files or * for all)
ifeq ($(API_TEST_FILE),)
	API_TEST_FILE=*.yaml
endif

# Deployment environment
ifeq ($(DEPLOY_ENV),)
	DEPLOY_ENV=int
endif

# Docker repository for the DEV environment
DOCKER_REGISTRY_DEV=

# Docker repository for the QA environment
DOCKER_REGISTRY_QA=

# Docker repository for the PROD environment
DOCKER_REGISTRY_PROD=

# Docker repository for the current environment
DOCKER_REGISTRY=

# Docker repository from where to pull the image
DOCKER_REGISTRY_PULL=

# Docker repository where to push the image
DOCKER_REGISTRY_PUSH=

# Command used to login into the Docker pull repository
# Example: "aws --profile MYENVPROFILE ecr get-login --no-include-email --region ${AWS_REGION} | sed 's|https://||'"
DOCKER_LOGIN_PULL="echo"

# Command used to login into the Docker push repository
# Example: "aws --profile MYENVPROFILE ecr get-login --no-include-email --region ${AWS_REGION} | sed 's|https://||'"
DOCKER_LOGIN_PUSH="echo"

ECR_PROFILE_PULL=
ECR_PROFILE_PUSH=

# INT - integration environment via docker-compose
ifeq ($(DEPLOY_ENV), int)
	#ECR_PROFILE_PULL=
	#ECR_PROFILE_PUSH=
	DOCKER_REGISTRY=${DOCKER_REGISTRY_DEV}
	#DOCKER_REGISTRY_PULL=
	#DOCKER_REGISTRY_PUSH=
	NURAGOEXAMPLE_MONITORING_URL=http://nuragoexample:8071
	NURAGOEXAMPLE_PRIVATE_URL=http://nuragoexample:8072
	NURAGOEXAMPLE_PUBLIC_URL=http://nuragoexample:8073
	API_TEST_FILE=*.yaml
endif

# local environment via docker-compose
ifeq ($(DEPLOY_ENV), local)
	#ECR_PROFILE_PULL=
	#ECR_PROFILE_PUSH=
	DOCKER_REGISTRY=${DOCKER_REGISTRY_DEV}
	#DOCKER_REGISTRY_PULL=
	#DOCKER_REGISTRY_PUSH=
	NURAGOEXAMPLE_MONITORING_URL=http://nuragoexample:8071
	NURAGOEXAMPLE_PRIVATE_URL=http://nuragoexample:8072
	NURAGOEXAMPLE_PUBLIC_URL=http://nuragoexample:8073
	API_TEST_FILE=*.yaml
endif

# Development environment
ifeq ($(DEPLOY_ENV), dev)
	#ECR_PROFILE_PULL=
	#ECR_PROFILE_PUSH=
	DOCKER_REGISTRY=${DOCKER_REGISTRY_DEV}
	#DOCKER_REGISTRY_PULL=
	DOCKER_REGISTRY_PUSH=${DOCKER_REGISTRY_DEV}
	NURAGOEXAMPLE_MONITORING_URL=http://nuragoexample:8071
	NURAGOEXAMPLE_PRIVATE_URL=http://nuragoexample:8072
	NURAGOEXAMPLE_PUBLIC_URL=http://nuragoexample:8073
	API_TEST_FILE=*.yaml
endif

# QA environment
ifeq ($(DEPLOY_ENV), qa)
	#ECR_PROFILE_PULL=
	#ECR_PROFILE_PUSH=
	DOCKER_REGISTRY=${DOCKER_REGISTRY_QA}
	DOCKER_REGISTRY_PULL=${DOCKER_REGISTRY_DEV}
	DOCKER_REGISTRY_PUSH=${DOCKER_REGISTRY_QA}
	NURAGOEXAMPLE_MONITORING_URL=http://nuragoexample:8071
	NURAGOEXAMPLE_PRIVATE_URL=http://nuragoexample:8072
	NURAGOEXAMPLE_PUBLIC_URL=http://nuragoexample:8073
endif

# Production environment
ifeq ($(DEPLOY_ENV), prod)
	#ECR_PROFILE_PULL=
	#ECR_PROFILE_PUSH=
	DOCKER_REGISTRY=${DOCKER_REGISTRY_PROD}
	DOCKER_REGISTRY_PULL=${DOCKER_REGISTRY_QA}
	DOCKER_REGISTRY_PUSH=${DOCKER_REGISTRY_PROD}
	NURAGOEXAMPLE_MONITORING_URL=http://nuragoexample:8071
	NURAGOEXAMPLE_PRIVATE_URL=http://nuragoexample:8072
	NURAGOEXAMPLE_PUBLIC_URL=http://nuragoexample:8073
	API_TEST_FILE=*.yaml
endif

# Retry the make target (1) for (2) times each (3) seconds.
define make_retry
	ret=1 ; num=1 ; while [[ $${num} -le $(2) ]] ; do \
		$(MAKE) $(1) && ret=0 && break ; \
		((num = num + 1)) ; \
		sleep $(3); \
	done ; exit $${ret}
endef

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
	DEVMODE=LOCAL $(MAKE) format clean mod deps gendoc generate qa build docker dockertest

## Execute API tests
.PHONY: apitest
apitest:
	$(MAKE) venomtest API_TEST_DIR=monitoring API_TEST_URL="${NURAGOEXAMPLE_MONITORING_URL}" API_TEST_FILE=api.yaml
	$(MAKE) venomtest API_TEST_DIR=private API_TEST_URL="${NURAGOEXAMPLE_PRIVATE_URL}" API_TEST_FILE=${API_TEST_FILE}
	$(MAKE) venomtest API_TEST_DIR=public API_TEST_URL="${NURAGOEXAMPLE_PUBLIC_URL}" API_TEST_FILE=${API_TEST_FILE}

## Full build and test sequence
.PHONY: buildall
buildall: build qa docker

## Compile the application
.PHONY: build
build:
	CGO_ENABLED=0 $(GOBUILDENV) \
	$(GO) build \
	-tags ${STATIC_TAG} \
	-ldflags '-w -s -X main.programVersion=${VERSION} -X main.programRelease=${RELEASE} -extldflags "-fno-PIC ${STATIC_FLAG}"' \
	-o "./target/${BINPATH}$(PROJECT)" "$(CMDDIR)"

## Remove any build artifact
.PHONY: clean
clean:
	rm -rf "$(TARGETDIR)"

## Check the configuration files
.PHONY: confcheck
confcheck:
	${CONFCHECKCMD} "resources/test/etc/${PROJECT}/config.json"
	${CONFCHECKCMD} "resources/etc/${PROJECT}/config.json"

## Generate the coverage report
.PHONY: coverage
coverage: ensuretarget
	$(GO) tool cover -html="$(TARGETDIR)/report/coverage.out" -o "$(TARGETDIR)/report/coverage.html"

### Build everything inside a Docker container
.PHONY: dbuild
dbuild: clean ensuretarget dockerdev
	@echo 0 > "$(TARGETDIR)/make.exit"
	CVSPATH=$(CVSPATH) VENDOR=$(LCVENDOR) PROJECT=$(PROJECT) MAKETARGET='$(MAKETARGET)' DOCKERTAG='$(DOCKERTAG)' "$(CURRENTDIR)dockerbuild.sh"
	@exit `cat $(TARGETDIR)/make.exit`


## Build a DEB package
.PHONY: deb
deb:
	rm -rf "$(PATHDEBPKG)"
	$(MAKE) install DESTDIR="$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)"
	rm -f "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/$(DOCPATH)LICENSE"
	tar -zcvf "$(PATHDEBPKG)/$(PKGNAME)_$(VERSION).orig.tar.gz" -C "$(PATHDEBPKG)/" "$(PKGNAME)-$(VERSION)"
	cp -rf ./resources/debian "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian"
	mkdir -p "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/missing-sources"
	echo "// fake source for lintian" > "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/missing-sources/$(PROJECT).c"
	find "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/" -type f -exec sed $(SEDINPLACE) "s/~#DATE#~/`date -R`/" {} \;
	find "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/" -type f -exec sed $(SEDINPLACE) "s/~#PKGNAME#~/$(PKGNAME)/" {} \;
	find "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/" -type f -exec sed $(SEDINPLACE) "s/~#VERSION#~/$(VERSION)/" {} \;
	find "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/" -type f -exec sed $(SEDINPLACE) "s/~#RELEASE#~/$(RELEASE)/" {} \;
	echo "$(BINPATH)" > "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).dirs"
	echo "$(BINPATH)* $(BINPATH)" > "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/install"
	echo "$(DOCPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).dirs"
	echo "$(DOCPATH)* $(DOCPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/install"
ifneq ($(strip $(INITPATH)),)
	echo "$(INITPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).dirs"
	echo "$(INITPATH)* $(INITPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/install"
endif
ifneq ($(strip $(CONFIGPATH)),)
	echo "$(CONFIGPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).dirs"
	echo "$(CONFIGPATH)* $(CONFIGPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/install"
endif
ifneq ($(strip $(MANPATH)),)
	echo "$(MANPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).dirs"
	echo "$(MANPATH)* $(MANPATH)" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/install"
endif
	echo "statically-linked-binary [$(BINPATH)$(PROJECT)]" > "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).lintian-overrides"
	echo "new-package-should-close-itp-bug" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).lintian-overrides"
	echo "hardening-no-relro [$(BINPATH)$(PROJECT)]" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).lintian-overrides"
	echo "embedded-library [$(BINPATH)$(PROJECT)]: libyaml" >> "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)/debian/$(PKGNAME).lintian-overrides"
	cd "$(PATHDEBPKG)/$(PKGNAME)-$(VERSION)" && debuild -us -uc

## Get dependencies
.PHONY: deps
deps: ensuretarget
	curl --silent --show-error --fail --location "https://golangci-lint.run/install.sh" | sh -s -- -b "$(BINUTIL)" $(GOLANGCILINTVERSION)

## Build a scratch docker container to run this service
.PHONY: docker
docker: dockerdir dockerbuild dockerdb

### Build the docker container in the target/DOCKER directory
.PHONY: dockerbuild
dockerbuild:
	$(DOCKER) build \
	--no-cache \
	--tag=${LCVENDOR}/${PROJECT}$(DOCKERSUFFIX):latest \
	$(PATHDOCKERPKG)

### Generate the FlyWay container to update the database
.PHONY: dockerdb
dockerdb:
	cd resources/db && \
	$(DOCKER) build \
	--no-cache \
	--build-arg DEPLOY_ENV=${DEPLOY_ENV} \
	--build-arg DBTYPE="mysql" \
	--file db.dockerfile \
	--tag=${LCVENDOR}/${PROJECT}_db:latest \
	.

### Delete the Docker image
.PHONY: dockerdelete
dockerdelete:
	$(DOCKER) rmi -f `$(DOCKER) images "${LCVENDOR}/${PROJECT}$(DOCKERSUFFIX)" -q`

### Build a base development Docker image
.PHONY: dockerdev
dockerdev:
ifeq ($(DEPLOY_ENV),int)
	$(DOCKER) build --pull --tag "${LCVENDOR}/dev_${PROJECT}:dev" --file ./resources/docker/Dockerfile.dev ./resources/docker/
endif

## Create the directory with docker files to be packaged
.PHONY: dockerdir
dockerdir:
ifneq ($(GOOSARCH),linux$(DOCKERARCH))
	$(MAKE) build GOBUILDENV=$(LINUXGOBUILDENV)
endif
ifneq ($(GOBUILDENV),)
	$(MAKE) build GOBUILDENV=$(LINUXGOBUILDENV)
endif
	rm -rf "$(PATHDOCKERPKG)"
	$(MAKE) install DESTDIR="$(PATHDOCKERPKG)"
	$(MAKE) installssl DESTDIR="$(PATHDOCKERPKG)"
	cp resources/docker/Dockerfile.run "$(PATHDOCKERPKG)/Dockerfile"

## Login into Docker AWS ECR
.PHONY: ecrlogin
ecrlogin:
ifeq ($(ECR_REGISTRY),)
    # skip login
else
# check the main version of aws-cli
ifeq ($(shell aws --version 2>&1 | cut -d " " -f1 | cut -d "/" -f2 | cut -d "." -f1), 1)
	$(shell aws $(ECR_PROFILE) ecr get-login --no-include-email --region $(AWS_DEFAULT_REGION) | sed 's|https://||')
else
	aws "$(ECR_PROFILE)" ecr get-login-password --region $(AWS_DEFAULT_REGION) | $(DOCKER) login --password-stdin --username AWS $(ECR_REGISTRY)
endif
endif

## Promote docker image from DEV to PROD repository
.PHONY: dockerpromote
dockerpromote:
	$(MAKE) ecrlogin ECR_PROFILE="${ECR_PROFILE_PULL}" ECR_REGISTRY="${DOCKER_REGISTRY_PULL}"
	$(DOCKER) pull "${DOCKER_REGISTRY_PULL}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)"
	$(DOCKER) tag "${DOCKER_REGISTRY_PULL}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)" "${DOCKER_REGISTRY_PUSH}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)"
	$(MAKE) ecrlogin ECR_PROFILE="${ECR_PROFILE_PUSH}" ECR_REGISTRY="${DOCKER_REGISTRY_PUSH}"
	$(DOCKER) push "${DOCKER_REGISTRY_PUSH}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)"
	$(DOCKER) rmi -f `$(DOCKER) images "${DOCKER_REGISTRY_PULL}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)" -q`

## Push docker container to a remote repository
.PHONY: dockerpush
dockerpush:
	$(MAKE) ecrlogin ECR_PROFILE="${ECR_PROFILE_PUSH}" ECR_REGISTRY="${DOCKER_REGISTRY_PUSH}"
	$(DOCKER) tag "${LCVENDOR}/${PROJECT}$(DOCKERSUFFIX):latest" "${DOCKER_REGISTRY_PUSH}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)"
	$(DOCKER) push "${DOCKER_REGISTRY_PUSH}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):$(DOCKERTAG)"
	$(DOCKER) tag "${LCVENDOR}/${PROJECT}$(DOCKERSUFFIX):latest" "${DOCKER_REGISTRY_PUSH}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):latest"
	$(DOCKER) push "${DOCKER_REGISTRY_PUSH}/${DOCKERPREFIX}${PROJECT}$(DOCKERSUFFIX):latest"

## Start a local docker compose environment for manual testing
.PHONY: localenvstart
localenvstart:
	$(MAKE) dcenvup DEPLOY_ENV=local

## Stop the local docker compose environment
.PHONY: localenvstop
localenvstop:
	$(MAKE) dcenvdown DEPLOY_ENV=local

## Test the newly built docker container
.PHONY: dockertest
dockertest:
	$(MAKE) dcenvup dcenvdown DEPLOY_ENV=int

## Start a docker-compose environment
.PHONY: dcenvup
dcenvup: dockerenvcfg dockerdev
	test -f "$(BINUTIL)/dockerize" || curl --silent --show-error --fail --location "https://github.com/jwilder/dockerize/releases/download/${DOCKERIZEVERSION}/dockerize-linux-$(DOCKERARCH)-${DOCKERIZEVERSION}.tar.gz" | tar -xz -C "$(BINUTIL)"
	@echo 0 > "$(TARGETDIR)/make.exit"
	$(DOCKERCOMPOSECMD) -f "./docker-compose-$(DEPLOY_ENV).yml" down --volumes || true
	$(DOCKERCOMPOSECMD) -f "./docker-compose-$(DEPLOY_ENV).yml" build $(DOCKERBUILDARG)
ifeq ($(DEPLOY_ENV),int)
	$(DOCKERCOMPOSECMD) -f "./docker-compose-$(DEPLOY_ENV).yml" run ${PROJECT}_integration || echo $${?} > "$(TARGETDIR)/make.exit"
else
	$(DOCKERCOMPOSECMD) -f "./docker-compose-$(DEPLOY_ENV).yml" up
endif
	$(DOCKERCOMPOSECMD) -f "./docker-compose-$(DEPLOY_ENV).yml" logs

## Stop a docker-compose environment
.PHONY: dcenvdown
dcenvdown:
	$(DOCKERCOMPOSECMD) -f "./docker-compose-$(DEPLOY_ENV).yml" down --rmi local --volumes --remove-orphans || true
	@exit `cat $(TARGETDIR)/make.exit`

## Configure environment for docker-compose integration tests
.PHONY: dockerenvcfg
dockerenvcfg: ensuretarget
	@echo "NURAGOEXAMPLE_REMOTECONFIGPROVIDER=envvar" > "$(TARGETDIR)/nuragoexample.cfg.env"
	@echo "NURAGOEXAMPLE_REMOTECONFIGDATA=$(shell cat resources/test/$(DEPLOY_ENV)/nuragoexample/config.json | base64 | tr -d \\n)" >> "$(TARGETDIR)/nuragoexample.cfg.env"

## Create the target directories if missing
.PHONY: ensuretarget
ensuretarget:
	@mkdir -p "$(TARGETDIR)/test"
	@mkdir -p "$(TARGETDIR)/report"
	@mkdir -p "$(BINUTIL)"

## Format the source code
.PHONY: format
format:
	@find "$(CMDDIR)" -type f -name "*.go" -exec $(GOFMT) -s -w {} \;
	@find "$(SRCDIR)" -type f -name "*.go" -exec $(GOFMT) -s -w {} \;

## Generate go code automatically
.PHONY: generate
generate:
	rm -f internal/mocks/*.go
	$(GO) generate $(GOPKGS)

## Generate static documentation from /doc/src
.PHONY: gendoc
gendoc:
	yq --input-format yaml --output-format json < doc/src/config.yaml . > doc/src/config.json
	#jv doc/src/config.schema.json doc/src/config.json
	ASSUME_NO_MOVING_GC_UNSAFE_RISK_IT_WITH=$(GOVERSION) \
	gomplate \
	--datasource config=./doc/src/config.json \
	--template description=./doc/src/description.tmpl \
	--template development=./doc/src/development.tmpl \
	--template structure=./doc/src/structure.tmpl \
	--template deployment=./doc/src/deployment.tmpl \
	--file ./doc/src/README.tmpl \
	--out README.md

## Install this application
.PHONY: install
install: uninstall
	mkdir -p "$(PATHINSTBIN)"
	cp -r "./target/${BINPATH}"* "$(PATHINSTBIN)"
	find "$(PATHINSTBIN)" -type d -exec chmod 755 {} \;
	find "$(PATHINSTBIN)" -type f -exec chmod 755 {} \;
	mkdir -p "$(PATHINSTDOC)"
	cp -f ./LICENSE "$(PATHINSTDOC)"
	cp -f ./README.md "$(PATHINSTDOC)"
	cp -f ./VERSION "$(PATHINSTDOC)"
	cp -f ./RELEASE "$(PATHINSTDOC)"
	cp -f ./doc/*.md "$(PATHINSTDOC)"
	chmod -R 644 "$(PATHINSTDOC)"*
ifneq ($(strip $(INITPATH)),)
	mkdir -p "$(PATHINSTINIT)"
	cp -rf "./resources/${INITPATH}"* "$(PATHINSTINIT)"
	find "$(PATHINSTINIT)" -type d -exec chmod 755 {} \;
	find "$(PATHINSTINIT)" -type f -exec chmod 644 {} \;
endif
ifneq ($(strip $(CONFIGPATH)),)
	mkdir -p "$(PATHINSTCFG)"
	touch -c "$(PATHINSTCFG)"*
	cp -rf "./resources/${CONFIGPATH}"* "$(PATHINSTCFG)"
	find "$(PATHINSTCFG)" -type d -exec chmod 755 {} \;
	find "$(PATHINSTCFG)" -type f -exec chmod 644 {} \;
endif
ifneq ($(strip $(MANPATH)),)
	mkdir -p "$(PATHINSTMAN)"
	cat "./resources/${MANPATH}${PROJECT}.1" | gzip -9 > "$(PATHINSTMAN)${PROJECT}.1.gz"
	find "$(PATHINSTMAN)" -type f -exec chmod 644 {} \;
endif
# only write the nonroot passwd file for staged installs (e.g. Docker scratch image), never on a live system
ifneq ($(strip $(DESTDIR)),)
	echo 'nonroot:*:65532:65532:nonroot:/nonexistent:/bin/false' > "$(DESTDIR)/etc/passwd"
endif

## Install TLS root CA certificates
.PHONY: installssl
installssl:
ifneq ($(strip $(SSLCONFIGPATH)),)
	# add system root CA certificates
	for CERT in ${CACERTPATH} ; do \
		test -f "$${CERT}" && \
		mkdir -p "$${DESTDIR}$$(dirname "$${CERT}")" && \
		cp "$${CERT}" "$${DESTDIR}$${CERT}" && \
		break ; \
	done
	# add custom CA certificates
	mkdir -p "$(PATHINSTSSLCFG)"
	cp -r "./resources/${SSLCONFIGPATH}"* "$(PATHINSTSSLCFG)"
	rm "$(PATHINSTSSLCFG)certs/.keep"
	find "$(PATHINSTSSLCFG)" -type d -exec chmod 755 {} \;
	find "$(PATHINSTSSLCFG)" -type f -exec chmod 644 {} \;
endif

## Check dependencies for known vulnerabilities
.PHONY: govulncheck
govulncheck:
	@echo -e "\n\n>>> START: Vulnerability check <<<\n\n"
	$(GOVULNCHECK) "$(CMDDIR)/..." "$(SRCDIR)/..."
	@echo -e "\n\n>>> END: Vulnerability check <<<\n\n"

## Check code against multiple linters
.PHONY: linter
linter:
	@echo -e "\n\n>>> START: Static code analysis <<<\n\n"
	$(GOLANGCILINT) run --max-issues-per-linter 0 --max-same-issues 0 "$(CMDDIR)/..." "$(SRCDIR)/..."
	@echo -e "\n\n>>> END: Static code analysis <<<\n\n"

## Download dependencies
.PHONY: mod
mod: gotools
	$(GO) mod download all

## Test the OpenAPI specification
.PHONY: openapitest
openapitest:
	$(MAKE) schemathesistest API_TEST_URL="${NURAGOEXAMPLE_MONITORING_URL}" OPENAPI_FILE=openapi_monitoring.yaml
	$(MAKE) schemathesistest API_TEST_URL="${NURAGOEXAMPLE_PRIVATE_URL}" OPENAPI_FILE=openapi_private.yaml
	$(MAKE) schemathesistest API_TEST_URL="${NURAGOEXAMPLE_PUBLIC_URL}" OPENAPI_FILE=openapi_public.yaml

## Ping the deployed service
.PHONY: ping
ping:
	if [ "200_$(VERSION)_$(RELEASE)_" != "$(shell curl --silent --insecure '$(NURAGOEXAMPLE_MONITORING_URL)/ping' | jq -r '.code,.version,.release' | tr '\n' '_')" ]; then exit 1; fi

## Run all tests and static analysis tools
.PHONY: qa
qa: linter govulncheck confcheck test coverage

## Retry the ping command automatically
.PHONY: rping
rping:
	$(call make_retry,ping,60,5)

## Build an RPM package
.PHONY: rpm
rpm:
	@test $(words $(CURDIR)) -eq 1 || { echo "ERROR: rpmbuild does not support spaces in the project path: $(CURDIR)"; exit 1; }
	rm -rf "$(PATHRPMPKG)"
	rpmbuild \
	--define "_topdir $(PATHRPMPKG)" \
	--define "_vendor $(VENDOR)" \
	--define "_owner $(OWNER)" \
	--define "_project $(PROJECT)" \
	--define "_package $(PKGNAME)" \
	--define "_version $(VERSION)" \
	--define "_release $(RELEASE)" \
	--define "_current_directory $(CURRENTDIR)" \
	--define "_binpath /$(BINPATH)" \
	--define "_docpath /$(DOCPATH)" \
	--define "_configpath /$(CONFIGPATH)" \
	--define "_initpath /$(INITPATH)" \
	--define "_manpath /$(MANPATH)" \
	-bb resources/rpm/rpm.spec

## Test the OpenAPI specification against the real deployed service
.PHONY: schemathesistest
schemathesistest:
	schemathesis run \
	--checks=all \
	--request-timeout=2000 \
	--max-examples=100 \
	--url='${API_TEST_URL}' \
	${OPENAPI_FILE}

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

## Run benchmarks (real measurements, without -race or coverage)
.PHONY: bench
bench: ensuretarget
	@echo -e "\n\n>>> START: Benchmarks <<<\n\n"
	$(GOTEST) \
	-tags=unit,benchmark \
	-run=^$$ \
	-bench=. \
	-benchmem \
	-v $(GOPKGS)
	@echo -e "\n\n>>> END: Benchmarks <<<\n\n"

## Get the go tools
.PHONY: gotools
gotools:
	$(GO) get -tool go.uber.org/mock/mockgen@latest
	$(GO) get -tool golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) install github.com/jstemmer/go-junit-report/v2@latest
	$(GO) install github.com/hairyhenderson/gomplate/v4/cmd/gomplate@latest
	$(GO) install github.com/mikefarah/yq/v4@latest
	$(GO) install github.com/santhosh-tekuri/jsonschema/cmd/jv@latest

## Remove all installed files (excluding configuration files)
.PHONY: uninstall
uninstall:
	rm -rf "$(PATHINSTBIN)$(PROJECT)"
	rm -rf "$(PATHINSTDOC)"

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

## Update golangci-lint version
.PHONY: updatelint
updatelint:
	$(eval LAST_GOLANGCILINT_VERSION=$(shell curl -sL https://github.com/golangci/golangci-lint/releases/latest | sed -n 's/.*<title>Release \(v[0-9]*\.[0-9]*\.[0-9]*\).*/\1/p'))
	sed $(SEDINPLACE) "s|^GOLANGCILINTVERSION=v[0-9]*\.[0-9]*\.[0-9]*$$|GOLANGCILINTVERSION=${LAST_GOLANGCILINT_VERSION}|g" Makefile

## Update dependencies
.PHONY: updatemod
updatemod: mod
	$(GO) get -t -u ./... && \
	go mod tidy -compat=$(shell sed -n -E 's/^go ([0-9]+\.[0-9]+).*/\1/p' go.mod)

## Run venom tests
.PHONY: venomtest
venomtest:
	@mkdir -p "$(TARGETDIR)/report/${DEPLOY_ENV}/venom/$(API_TEST_DIR)"
	venom run \
		--var nuragoexample.url="${API_TEST_URL}" \
		--var nuragoexample.version="${VERSION}" \
		--var nuragoexample.release="${RELEASE}" \
		-vv \
		--output-dir=$(TARGETDIR)/report/${DEPLOY_ENV}/venom/$(API_TEST_DIR) \
		resources/test/venom/$(DEPLOY_ENV)/$(API_TEST_DIR)/$(API_TEST_FILE)

## Increase the patch number in the VERSION file
.PHONY: versionup
versionup:
	echo ${VERSION} | awk -F. '{printf("%d.%d.%d\n",$$1,$$2,(($$3+1)));}' > VERSION
