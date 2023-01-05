default: lint generate test

run *FLAGS:
  go run ./... {{FLAGS}}

test *FLAGS:
  go test {{FLAGS}} ./...

lint: gomod
  golangci-lint run

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

publish:
  ko publish -P

deploy *FLAGS:
  #!/usr/bin/env bash
  set -euxo pipefail

  image=$(just publish)
  helm upgrade agent-stack-k8s charts/agent-stack-k8s \
    --namespace buildkite \
    --install \
    --create-namespace \
    --wait \
    --set image=$image \
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
