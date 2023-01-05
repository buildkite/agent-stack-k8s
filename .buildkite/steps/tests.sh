#!/bin/bash
set -euxo pipefail

go version
go install gotest.tools/gotestsum

echo '+++ Running tests'
gotestsum -f standard-verbose --junitfile "junit-${BUILDKITE_JOB_ID}.xml" -- -count=1 -failfast "$@" ./...
