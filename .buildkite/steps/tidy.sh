#!/usr/bin/env sh

set -euf

echo --- :hammer: Installing tools
apk add --update-cache --no-progress git just

echo --- :broom: Checking tidiness of go modules
just gomod
