#!/bin/sh

set -eu

echo --- Installing packages
apk add --update-cache --no-progress skopeo

repo="docker.io/buildkite/agent"

echo --- Awking go.mod for agent version
agent_version="$(awk '/github\.com\/buildkite\/agent\/v3/ { print $2 }' go.mod | cut -c 2- )"
echo "Using agent version ${agent_version} as image tag"

echo --- :docker: Inspecting agent docker image manifest
digest=$(skopeo inspect "docker://${repo}:${agent_version}" --format {{.Digest}})

echo Choosing image "${repo}@${digest}"
buildkite-agent meta-data set agent-image "${repo}@${digest}"
