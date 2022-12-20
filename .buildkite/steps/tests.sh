#!/bin/bash
set -euo pipefail

go version
go install gotest.tools/gotestsum

echo '+++ Running tests'
gotestsum --junitfile "junit-${BUILDKITE_JOB_ID}.xml" -- -count=1 -failfast "$@" ./...
