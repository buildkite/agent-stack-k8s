default:
  just --list

run *FLAGS:
  go run ./... {{FLAGS}}

test *FLAGS:
  #!/usr/bin/env bash

  set -exufo pipefail

  GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

  go test \
    -ldflags="-X github.com/buildkite/agent-stack-k8s/v2/internal/integration_test.branch=${GIT_BRANCH}" \
    {{FLAGS}} \
    ./...

lint *FLAGS: gomod
  golangci-lint run {{FLAGS}}

generate:
  go generate ./...

# requires $GITHUB_TOKEN to be set to a personal access token with access to the buildkite repository
get-schema:
  wget --header "Authorization: token $GITHUB_TOKEN" \
    -O internal/integration/api/schema.graphql \
    https://raw.githubusercontent.com/buildkite/buildkite/main/lib/graphql/schema.graphql

gomod:
  #!/usr/bin/env sh
  set -euf

  go mod tidy

  # TODO: remove `-G.` once chmod 777 issue is fixed
  if ! git diff -G. --no-ext-diff --exit-code go.mod go.sum; then
    echo "Run"
    echo "  go mod tidy"
    echo "and make a commit."

    exit 1
  fi

controller *FLAGS:
  #!/usr/bin/env bash
  set -eufo pipefail

  export VERSION=$(git describe)
  ko build --preserve-import-paths {{FLAGS}}

deploy *FLAGS:
  #!/usr/bin/env bash
  set -euxo pipefail

  helm upgrade agent-stack-k8s charts/agent-stack-k8s \
    --namespace buildkite \
    --install \
    --create-namespace \
    --wait \
    {{FLAGS}}

# Invoke with CLEANUP_PIPELINES=true
# pass in --org=<org slug of k8s pipeline> --buildkite-token=<graphql-token> or use environment variables per development.md
cleanup-orphans *FLAGS:
  #!/usr/bin/env bash
  set -e
  export CLEANUP_PIPELINES=true
  go test -v \
    -ldflags="-X github.com/buildkite/agent-stack-k8s/v2/internal/integration_test.branch=${GIT_BRANCH}" \
    -run TestCleanupOrphanedPipelines \
    ./internal/integration \
    {{FLAGS}}
