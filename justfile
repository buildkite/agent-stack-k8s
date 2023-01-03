default: lint generate build test

build:
  echo Building…

run *FLAGS:
  go run ./... {{FLAGS}}

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

agent repo=("ghcr.io/buildkite/agent-k8s") tag=("latest"):
  #!/usr/bin/env bash
  set -euxo pipefail
  pushd agent/packaging/docker/alpine-linux
  for arch in arm64 amd64; do
    export GOOS=linux GOARCH=$arch
    go build -o buildkite-agent-linux-$arch github.com/buildkite/agent/v3
  done
  docker buildx build --tag {{repo}}:{{tag}} --platform linux/arm64,linux/amd64 --push .
  rm buildkite-agent-linux*

deploy *FLAGS:
  #!/usr/bin/env bash
  set -euxo pipefail

  image=$(go run github.com/google/ko@latest publish -P)
  helm upgrade agent-stack-k8s . \
    --namespace buildkite \
    --install \
    --create-namespace \
    --wait \
    --set image=$image \
    {{FLAGS}}
