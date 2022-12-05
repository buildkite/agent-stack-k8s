default: lint generate build test

build:
  echo Building…

test *FLAGS:
  go test {{FLAGS}} ./...

lint: gomod

generate:
    go run github.com/Khan/genqlient api/genqlient.yaml

gomod:
  #!/usr/bin/env bash
  set -euo pipefail
  go mod tidy
  git diff --no-ext-diff --quiet --exit-code --name-only go.mod go.sum

agent:
  #!/usr/bin/env bash
  set -euo pipefail
  export GOOS=linux GOARCH=amd64
  cd agent/packaging/docker/alpine-linux
  go build -o buildkite-agent github.com/buildkite/agent/v3
  docker buildx build --tag benmoss/buildkite-agent:latest --platform linux/amd64,linux/arm64 --push .
