#!/usr/bin/env sh

set -euf

echo --- :hammer: Installing tools
apk add --update-cache --no-progress git just aws-cli jq

. .buildkite/steps/assume-role.sh

echo --- :hammer: Restoring cache
buildkite-agent cache restore --debug

export GOCACHE="$(pwd)/.cache/go-build"
export GOMODCACHE="$(pwd)/.cache/go-mod"

go env

echo --- :broom: Checking tidiness of go modules
just gomod

echo --- :hammer: Saving cache
buildkite-agent cache save --debug
