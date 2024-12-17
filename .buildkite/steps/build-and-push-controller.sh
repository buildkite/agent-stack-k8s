#!/usr/bin/env bash

set -Eeufo pipefail

echo --- Installing packages
apt-get update && apt-get install -y --no-install-recommends awscli jq

echo --- Installing ko
KO_VERSION="0.13.0"
OS="$(go env GOOS)"
ARCH="$(uname -m)"
curl -sSfL "https://github.com/ko-build/ko/releases/download/v${KO_VERSION}/ko_${KO_VERSION}_${OS^}_${ARCH}.tar.gz" | tar -xzv -C /bin ko

tag="$(git describe)"
version="${tag#v}"

# Is this a release version (version-tagged)?
if [[ "${version}" == "${BUILDKITE_TAG#v}" ]] ; then
    # Publish to both PECR and GHCR
    controller_repo_pecr="public.ecr.aws/buildkite/agent-stack-k8s/controller"
    controller_repo_ghcr="ghcr.io/buildkite/agent-stack-k8s/controller"
else
    # Publish dev images to PECR dev repo only
    controller_repo_pecr="public.ecr.aws/buildkite/agent-stack-k8s-dev/controller"
fi

if [[ "${controller_repo_pecr:-}" != "" ]] ; then
    . .buildkite/steps/assume-role.sh

    echo ~~~ Logging into Public ECR
    ko login public.ecr.aws -u AWS --password "$(aws --region us-east-1 ecr-public get-login-password)"

    echo --- Building with ko for Public ECR
    controller_image_pecr="$(
        VERSION="${tag}" \
        KO_DOCKER_REPO="${controller_repo_pecr}" \
            ko build --bare --tags "${version}" --platform linux/amd64,linux/arm64 \
    )"
    buildkite-agent meta-data set controller-image-pecr "${controller_image_pecr}"
fi
if [[ "${controller_repo_ghcr:-}" != "" ]] ; then
    echo --- Logging into to GHCR
    ko login ghcr.io -u "${REGISTRY_USERNAME}" --password "${REGISTRY_PASSWORD}"

    echo --- Building with ko for GHCR
    controller_image_ghcr="$(
        VERSION="${tag}" \
        KO_DOCKER_REPO="${controller_repo_ghcr}" \
            ko build --bare --tags "${version}" --platform linux/amd64,linux/arm64 \
    )"
    buildkite-agent meta-data set controller-image-ghcr "${controller_image_ghcr}"
fi

buildkite-agent annotate --style success --append <<EOF
### Controller
----------------------------------------------------------------------------
| Version    | Image                                                       |
|------------|-------------------------------------------------------------|
| ${version} | ${controller_image_pecr:-} <br>  ${controller_image_ghcr:-} |
----------------------------------------------------------------------------
EOF
