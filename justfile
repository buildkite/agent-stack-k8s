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
    go generate ./...

gomod:
  #!/usr/bin/env bash
  set -euo pipefail
  go mod tidy
  git diff --no-ext-diff --exit-code go.mod go.sum

agent target os=("linux") arch=("amd64 arm64"):
  #!/usr/bin/env bash
  set -euxo pipefail
  pushd agent
  version=$(git describe --tags)
  platforms=()
  for os in {{os}}; do
    for arch in {{arch}}; do
      platforms+=("${os}/${arch}")
      ./scripts/build-binary.sh $os $arch $version
    done
  done
  commaified=$(IFS=, ; echo "${platforms[*]}")
  mkdir -p {{justfile_directory()}}/dist
  mv pkg/buildkite-agent-* packaging/docker/alpine/
  docker buildx build --tag {{target}} --platform "$commaified" --push --metadata-file {{justfile_directory()}}/dist/metadata.json packaging/docker/alpine
  rm packaging/docker/alpine/buildkite-agent-*

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
