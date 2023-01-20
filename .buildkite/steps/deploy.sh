#!/bin/ash
set -euxo pipefail

apk add helm yq skopeo git --quiet --no-progress

tag=$(git describe)
version=$(echo "$tag" | sed 's/v//')
temp_agent_image=$(buildkite-agent meta-data get "agent-image")
agent_image="${KO_DOCKER_REPO}/agent-k8s:${tag}"
controller_image=$(buildkite-agent meta-data get "controller-image")

set +x
skopeo login ghcr.io -u $REGISTRY_USERNAME --password $REGISTRY_PASSWORD --authfile ~/.docker/config.json
set -x

skopeo copy "docker://${temp_agent_image}" "docker://${agent_image}" --authfile ~/.docker/config.json

yq -i ".image = \"$controller_image\"" charts/agent-stack-k8s/values.yaml
yq -i ".config.image = \"$agent_image\"" charts/agent-stack-k8s/values.yaml
helm package ./charts/agent-stack-k8s --app-version "$version" -d dist --version "$version"
helm_image="oci://${KO_DOCKER_REPO}/helm"
helm push ./dist/agent-stack-k8s-*.tgz ${helm_image}

set +x
helm upgrade agent-stack-k8s ${helm_image}/agent-stack-k8s \
    --version ${version} \
    --namespace buildkite \
    --install \
    --create-namespace \
    --wait \
    --set config.org=$BUILDKITE_ORGANIZATION_SLUG \
    --set agentToken=$BUILDKITE_AGENT_TOKEN \
    --set graphqlToken=$BUILDKITE_TOKEN \
    --set image=$controller_image \
    --set config.image=$agent_image
set -x
