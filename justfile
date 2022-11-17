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
