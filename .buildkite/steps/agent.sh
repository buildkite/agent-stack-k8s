#!/bin/bash
set -euxo pipefail

apk add just go jq

docker buildx create --driver kubernetes --use
just agent ttl.sh/buildkite-agent:1h

image=$(cat dist/metadata.json | jq -r '"ttl.sh/buildkite-agent@\(."containerimage.digest")"')
buildkite-agent meta-data set "image" "${image}"

docker buildx rm
