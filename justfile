default:
  just --list

# Running the controller locally using production buildkite as backend.
# This is sufficient for vast majority of issues.
run *FLAGS:
  go run ./... {{FLAGS}}

# Running the controller locally but with local bk/bk as the backend.
# This is probably only useful for Buildkite employees.
run-with-local-bk:
  #!/usr/bin/env bash

  set -exufo pipefail

  kubectl create namespace local-bk || true

  kubectl -n local-bk delete secret buildkite-agent-token || true
  kubectl -n local-bk create secret generic buildkite-agent-token \
    --from-literal=BUILDKITE_AGENT_TOKEN='bkct_buildkite'

  go run ./... --namespace local-bk --config=config.dev.yaml

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

# Checking to see if the current k8s user has enough permission to run integration test in the current context cluster.
check-k8s-api-access:
  ./utils/check-rbac.sh
