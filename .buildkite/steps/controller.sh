#!/usr/bin/env bash
set -euxo pipefail

apt update && apt install -y --no-install-recommends jq

OS=$(go env GOOS)
ARCH=$(go env GOARCH)
if [ "$ARCH" == "amd64" ]; then
    ARCH="x86_64"
fi
latest=$(curl -L -s https://api.github.com/repos/ko-build/ko/releases/latest | jq -r '.tag_name')
curl -sSfL "https://github.com/ko-build/ko/releases/download/${latest}/ko_${latest:1}_${OS^}_${ARCH}.tar.gz" | tar -xzv -C /bin ko

tag=$(git describe)
ko login ghcr.io -u $REGISTRY_USERNAME --password $REGISTRY_PASSWORD
export KO_DOCKER_REPO=ghcr.io/buildkite/agent-stack-k8s/controller
controller_image=$(ko build --bare --tags "$tag" --platform linux/amd64,linux/arm64)

buildkite-agent meta-data set "controller-image" "${controller_image}"
