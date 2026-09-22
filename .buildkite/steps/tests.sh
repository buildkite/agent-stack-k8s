#!/usr/bin/env bash

set -eufo pipefail

# The informer-registration regression tests for
# https://github.com/buildkite/agent-stack-k8s/issues/951 only fail under the
# race detector, so run them with -race before the main suite. Scoped with
# -run because the rest of the package is not race-clean yet: NewJobWatcher and
# friends write package-level gauge funcs that parallel tests race on.
echo "+++ Race detector on informer registration :zap:"
go test -race -count=1 -run 'ReplaysWithValidContext' ./internal/controller/scheduler/

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
export AGENT_TOKEN_SECRET="agent-stack-k8s-secrets"

go tool gotestsum --junitfile "junit-${BUILDKITE_JOB_ID}.xml" -- \
  -count=1 \
  -ldflags="-X ${package}.branch=${branch}" \
  ./...
