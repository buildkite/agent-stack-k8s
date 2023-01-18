#!/bin/bash
set -euxo pipefail

apk add helm yq skopeo

tag=$(git describe)
version=$(echo "$tag" | sed 's/v//')
image=$(buildkite-agent meta-data get "image")

echo $REGISTRY_PASSWORD | skopeo login ghcr.io -u $REGISTRY_USERNAME --password-stdin
skopeo copy "docker://${image}" "docker://ghcr.io/buildkite/agent-k8s" --additional-tags "$tag"

helm package ./charts/agent-stack-k8s --app-version "$version" -d dist --version "$version"
helm push ./dist/agent-stack-k8s-*.tgz oci://{{repo}}

just deploy \
    --set config.org=buildkite-kubernetes-stack \
    --set agentToken=$BUILDKITE_AGENT_TOKEN \
    --set graphqlToken=$BUILDKITE_TOKEN
