#!/usr/bin/env bash

set -eufo pipefail

echo "--- Installing gotestsum :golang::test_tube:"
go install gotest.tools/gotestsum

echo '+++ Running integration tests :test_tube:'
package="github.com/buildkite/agent-stack-k8s/v2/internal/integration_test"
branch="${BUILDKITE_BRANCH:-main}"
IMAGE=$(buildkite-agent meta-data get "agent-image")
export IMAGE

args=(
  --junitfile "junit-${BUILDKITE_JOB_ID}.xml"
  -- \
  -count=1 \
  -failfast \
  -ldflags="-X ${package}.branch=${branch}" \
  ./...
)

if [[ "${TEST_PRESERVE_PIPELINES:-false}" == "true" ]]; then
  echo Preserving pipelines for debugging
  args+=(--preserve-pipelines)
fi

gotestsum "${args[@]}"
