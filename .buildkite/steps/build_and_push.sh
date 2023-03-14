#!/bin/ash

set -euo pipefail

echo --- :hammer: Installing tools
apk add helm yq skopeo git --quiet --no-progress

. .buildkite/steps/repo_info.sh

echo --- :docker: Logging into ghcr.io
skopeo login ghcr.io -u "$REGISTRY_USERNAME" --password "$REGISTRY_PASSWORD" --authfile ~/.docker/config.json

echo --- :docker: Copying image to ghcr.io
skopeo copy --multi-arch=all "docker://${temp_agent_image}" "docker://${agent_image}" --authfile ~/.docker/config.json

echo --- :helm: Packaging helm chart
yq -i ".image = \"$controller_image\"" charts/agent-stack-k8s/values.yaml
yq -i ".config.image = \"$agent_image\"" charts/agent-stack-k8s/values.yaml
helm package ./charts/agent-stack-k8s --app-version "$version" -d dist --version "$version"

echo --- :helm: Pushing helm chart to ghcr.io
helm push ./dist/agent-stack-k8s-*.tgz ${helm_repo}
