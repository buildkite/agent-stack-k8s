#!/usr/bin/env bash

set -Eeufo pipefail

echo --- Installing packages
apt update && apt install -y --no-install-recommends jq


echo --- Installing ko
KO_VERSION=0.13.0
OS=$(go env GOOS)
ARCH=$(uname -m)
curl -sSfL "https://github.com/ko-build/ko/releases/download/v${KO_VERSION}/ko_${KO_VERSION}_${OS^}_${ARCH}.tar.gz" | tar -xzv -C /bin ko

tag=$(git describe)


echo --- Logging into to docker registry
ko login ghcr.io -u "$REGISTRY_USERNAME" --password "$REGISTRY_PASSWORD"

echo --- Building with ko
export KO_DOCKER_REPO=ghcr.io/buildkite/agent-stack-k8s/controller
controller_image=$(VERSION="$tag" ko build --bare --tags "$tag" --platform linux/amd64,linux/arm64)

buildkite-agent meta-data set controller-image "$controller_image"
