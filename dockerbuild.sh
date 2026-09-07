#!/usr/bin/env bash
#
# dockerbuild.sh
#
# Build the software inside a Docker container
#
# @author      Nicola Asuni <info@tecnick.com>
# ------------------------------------------------------------------------------
set -e -u +x

# NOTES:
# This script requires Docker

# EXAMPLE USAGE:
# CVSPATH=project VENDOR=vendorname PROJECT=projectname MAKETARGET=buildall ./dockerbuild.sh

# Variables (parameters).
: ${CVSPATH:=project}
: ${VENDOR:=vendor}
: ${PROJECT:=project}
: ${DOCKERTAG:=dev}
: ${MAKETARGET:=format clean ensuretarget mod deps generate qa example}
: ${DOCKER:=$(which docker)}
: ${DOCKERDEV:=${VENDOR}/dev_${PROJECT}:${DOCKERTAG}}

# Build the base environment and keep it cached locally.
${DOCKER} build --pull --tag ${DOCKERDEV} --file ./resources/docker/Dockerfile.dev ./resources/docker/

# Define the project root path.
PRJPATH=/root/src/${CVSPATH}/${PROJECT}

# Remove the temporary Dockerfile even if this script fails.
trap 'rm -f Dockerfile.test' EXIT

# Generate a temporary Dockerfile to build and test the project
# NOTE: The exit status of the RUN command is stored to be returned later,
#       so in case of error we can continue without interrupting this script.
cat > Dockerfile.test <<- EOM
FROM ${DOCKERDEV}
RUN \\
echo "[user]" >> /root/.gitconfig \\
&& echo "	email = godev@example.com" >> /root/.gitconfig \\
&& echo "	name = godevlocaltestuser" >> /root/.gitconfig \\
&& mkdir -p ${PRJPATH}
COPY ./ ${PRJPATH}
WORKDIR ${PRJPATH}
RUN make ${MAKETARGET} || (echo \$? > target/make.exit)
HEALTHCHECK CMD go version || exit 1
EOM

# Define the temporary Docker image name.
DOCKER_IMAGE_NAME=${VENDOR}/build_${PROJECT}:${DOCKERTAG}

# Build the Docker image.
BUILDKIT_PROGRESS=plain \
${DOCKER} build \
--no-cache \
--tag ${DOCKER_IMAGE_NAME} \
--file Dockerfile.test .

# Start a container using the newly created Docker image.
CONTAINER_ID=$(${DOCKER} run -d ${DOCKER_IMAGE_NAME})

# Copy all build/test artifacts back to the host.
${DOCKER} cp ${CONTAINER_ID}:"${PRJPATH}/target" ./

# Remove the temporary container and image.
rm -f Dockerfile.test
${DOCKER} rm -f ${CONTAINER_ID} || true
${DOCKER} rmi -f ${DOCKER_IMAGE_NAME} || true
