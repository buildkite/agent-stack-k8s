#!/usr/bin/env ash

set -eufo pipefail

echo --- :hammer: Installing tools
apk add --update-cache --no-progress helm yq skopeo git

source .buildkite/steps/repo_info.sh

echo --- :docker: Logging into ghcr.io
skopeo login ghcr.io \
  -u "$REGISTRY_USERNAME" \
  --password "$REGISTRY_PASSWORD" \
  --authfile ~/.docker/config.json

echo --- :docker: Copying image to ghcr.io
skopeo copy \
  --authfile ~/.docker/config.json \
  --multi-arch=all \
  "docker://${temp_agent_image}" "docker://${agent_image}"

buildkite-agent annotate --style success --append <<EOF
### Agent
---------------------------------------------------
| Version  | Image                                |
|----------|--------------------------------------|
| $version | $agent_image                         |
---------------------------------------------------
EOF

echo --- :helm: Packaging helm chart
yq -i ".image = \"$controller_image\"" charts/agent-stack-k8s/values.yaml
yq -i ".config.image = \"$agent_image\"" charts/agent-stack-k8s/values.yaml
helm package charts/agent-stack-k8s --app-version "$version" -d dist --version "$version"

echo --- :helm: Pushing helm chart to ghcr.io
helm push "dist/agent-stack-k8s-${version}.tgz" "$helm_repo"

buildkite-agent annotate --style success --append <<EOF
### Helm Chart
--------------------------------------------------
| Version  | Image                               |
|----------|-------------------------------------|
| $version | $helm_repo/agent-stack-k8s:$version |
--------------------------------------------------
EOF
