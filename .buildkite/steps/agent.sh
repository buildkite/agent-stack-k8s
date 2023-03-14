#!/usr/bin/env bash

set -eufo pipefail

apt update && apt install -y --no-install-recommends ca-certificates curl gnupg lsb-release jq
mkdir -p /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian \
  $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list >/dev/null
apt update && apt install -y docker-ce-cli

curl --proto '=https' --tlsv1.3 -sSf https://just.systems/install.sh | bash -s -- --to /bin

docker buildx create --driver kubernetes --use
function cleanup {
    docker buildx rm
}
trap cleanup EXIT

just agent ttl.sh/buildkite-agent:1h

image=$(jq -r '"ttl.sh/buildkite-agent@\(."containerimage.digest")"' dist/metadata.json)
buildkite-agent meta-data set agent-image "$image"
