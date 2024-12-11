#!/usr/bin/env ash

set -eufo pipefail

echo --- :hammer: Installing tools
apk add --update-cache --no-progress aws-cli git helm jq skopeo yq

echo --- :git::docker: determining version and tags
source .buildkite/steps/repo_info.sh

# TODO: Once the agent is also pushed to PECR, specialise this per registry
yq -i ".config.image = \"${agent_image}\"" charts/agent-stack-k8s/values.yaml

if [[ "${helm_repo_pecr:-}" != "" ]] ; then
    source .buildkite/steps/assume-role.sh

    echo "~~~ :helm: Logging into Public ECR"
    helm registry login public.ecr.aws -u AWS --password "$(aws --region us-east-1 ecr-public get-login-password)"

    echo "--- :helm: Packaging helm chart for Public ECR"
    yq -i ".image = \"${controller_image_pecr}\"" charts/agent-stack-k8s/values.yaml
    helm package charts/agent-stack-k8s --app-version "${version}" -d dist --version "${version}"

    echo "--- :helm: Pushing helm chart to public.ecr.aws"
    helm push "dist/agent-stack-k8s-${version}.tgz" "${helm_repo_pecr}"
fi

if [[ "${helm_repo_ghcr:-}" != "" ]] ; then
    echo "--- :docker: Logging into ghcr.io"
    skopeo login ghcr.io \
        -u "${REGISTRY_USERNAME}" \
        --password "${REGISTRY_PASSWORD}" \
        --authfile ~/.docker/config.json

    echo "--- :helm: Packaging helm chart for GHCR"
    yq -i ".image = \"${controller_image_ghcr}\"" charts/agent-stack-k8s/values.yaml
    helm package charts/agent-stack-k8s --app-version "${version}" -d dist --version "${version}"

    echo "--- :helm: Pushing helm chart to ghcr.io"
    helm push "dist/agent-stack-k8s-${version}.tgz" "${helm_repo_ghcr}"
fi

buildkite-agent annotate --style success --append <<EOF
### Helm Chart
---------------------------------------------------------------
| Version    | Image                                          |
|------------|------------------------------------------------|
| ${version} | ${helm_image_pecr:-} <br> ${helm_image_ghcr:-} |
---------------------------------------------------------------
EOF
