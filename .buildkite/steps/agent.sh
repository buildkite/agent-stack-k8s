#!/bin/bash
set -euxo pipefail

apk add just go jq --quiet --no-progress

docker buildx create --driver kubernetes --use
function cleanup {
    docker buildx rm
}
trap cleanup EXIT

just agent ttl.sh/buildkite-agent:1h

image=$(cat dist/metadata.json | jq -r '"ttl.sh/buildkite-agent@\(."containerimage.digest")"')
buildkite-agent meta-data set "agent-image" "${image}"
