#!/usr/bin/env bash

set -eufo pipefail

echo "--- Installing gotestsum :golang::test_tube:"
go install gotest.tools/gotestsum

echo "+++ Running integration tests :test_tube:"
package="github.com/buildkite/agent-stack-k8s/v2/internal/integration_test"
branch="${BUILDKITE_BRANCH:-main}"
IMAGE="$(buildkite-agent meta-data get agent-image)"
export IMAGE
# Do NOT allow integration test workloads in the buildkite namespace.
# It causes confusion and complicates cleanup.
# The buildkite namespace is reserved for CI workloads.
# Integration tests will use their own namespace and out-of-cluster k8s controller to manage job pods.
export NAMESPACE="buildkite-k8s-integration-test"

gotestsum --junitfile "junit-${BUILDKITE_JOB_ID}.xml" -- \
  -count=1 \
  -ldflags="-X ${package}.branch=${branch}" \
  ./...
