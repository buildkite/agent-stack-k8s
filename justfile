default:
  just --list

run *FLAGS:
  go run ./... {{FLAGS}}

test *FLAGS:
  go test {{FLAGS}} ./...

lint *FLAGS: gomod
  golangci-lint run {{FLAGS}}

generate:
    go run github.com/Khan/genqlient api/genqlient.yaml

gomod:
  #!/usr/bin/env bash
  set -euo pipefail
  go mod tidy
  git diff --no-ext-diff --exit-code go.mod go.sum

agent target=("ghcr.io/buildkite/agent-k8s:latest") os=("linux") arch=("amd64 arm64"):
  #!/usr/bin/env bash
  set -euxo pipefail
  export CGO_ENABLED=0
  pushd agent/packaging/docker/alpine
  platforms=()
  for os in {{os}}; do
    for arch in {{arch}}; do
      platforms+=("${os}/${arch}")
      export GOOS=$os GOARCH=$arch
      go build -o buildkite-agent-${os}-${arch} github.com/buildkite/agent/v3
    done
  done
  commaified=$(IFS=, ; echo "${platforms[*]}")
  docker buildx build --tag {{target}} --platform "$commaified" --push --metadata-file {{justfile_directory()}}/dist/metadata.json .
  rm buildkite-agent-linux*

controller *FLAGS:
  ko build -P {{FLAGS}}

deploy *FLAGS:
  #!/usr/bin/env bash
  set -euxo pipefail

  helm upgrade agent-stack-k8s charts/agent-stack-k8s \
    --namespace buildkite \
    --install \
    --create-namespace \
    --wait \
    {{FLAGS}}


release repo=("ghcr.io/buildkite/helm"):
  #!/usr/bin/env bash
  set -euxo pipefail

  goreleaser release --rm-dist
  tag=$(git describe)
  version=$(echo "$tag" | sed 's/v//')
  helm package ./charts/agent-stack-k8s --app-version "$version" -d dist --version "$version"
  push=$(helm push ./dist/agent-stack-k8s-*.tgz oci://{{repo}} 2>&1)
  gh release view "$tag" --json body -q .body > dist/body.txt
  cat << EOF >> dist/body.txt
  ## Helm chart
  \`\`\`
  $push
  \`\`\`
  EOF
  gh release edit "$tag" -F dist/body.txt

cleanup-orphans:
  go test -v -run TestCleanupOrphanedPipelines ./integration --delete-orphaned-pipelines
