#!/usr/bin/env bash

set -e

# wait for resources to be available and run integration tests
dockerize \
    -timeout 120s \
    -wait tcp://nuragoexample:8073/ping \
    -wait tcp://nuragoexample:8074 \
    -wait http://nuragoexample_smocker_ipify:8081/version \
    -wait tcp://nuragoexample_mysql:3306 \
    echo

# configure smocker mocks for the ipify client
curl -s -XPOST \
  --header "Content-Type: application/x-yaml" \
  --data-binary "@resources/test/int/smocker/ipify_apitest.yaml" \
  http://nuragoexample_smocker_ipify:8081/mocks

# Run tests. The API tests come first: they assert on the seeded rows. The gRPC
# tests and the schemathesis run that follow create and delete rows of their own.
DEPLOY_ENV=int make apitest grpcapitest openapitest

# reset the report folder ownership to the host user/group.
chown -R ${HOST_OWNER} /workspace/target/report/
