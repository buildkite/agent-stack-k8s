#!/bin/sh

set -eu

echo --- Installing packages
apk add --update-cache --no-progress skopeo

repo="docker.io/buildkite/agent"

echo --- :docker: Inspecting agent docker image manifest
digest=$(skopeo inspect "docker://${repo}:stable" --format {{.Digest}})

echo Choosing image "${repo}@${digest}"
buildkite-agent meta-data set agent-image "${repo}@${digest}"
