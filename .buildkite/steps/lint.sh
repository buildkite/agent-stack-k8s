#!/bin/bash
set -euxo pipefail

curl --proto '=https' --tlsv1.3 -sSf https://just.systems/install.sh | bash -s -- --to /bin
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.50.1

just lint -v
