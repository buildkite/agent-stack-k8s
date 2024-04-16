#!/bin/sh

set -eu

echo --- Installing packages
apk add --update-cache --no-progress skopeo

repo="ghcr.io/buildkite/agent"

echo --- Awking go.mod for agent version
agent_version="$(awk '/github\.com\/buildkite\/agent\/v3/ { print $2 }' go.mod | cut -c 2- )"
echo "Using agent version ${agent_version} as image tag"

echo --- :docker: Inspecting agent docker image manifest
digest=$(skopeo inspect "docker://${repo}:${agent_version}" --format {{.Digest}})

agent_image="${repo}@${digest}"
echo Choosing image "${agent_image}"
buildkite-agent meta-data set agent-image "${agent_image}"

buildkite-agent annotate --style success --append <<EOF
### Agent
---------------------------------------------------
| Version        | Image                          |
|----------------|--------------------------------|
| $agent_version | $agent_image                   |
---------------------------------------------------
EOF
