#!/usr/bin/env sh

echo --- :hammer: Installing tools
apk add --update-cache --no-progress git just

echo --- :hammer: Checking tidiness of go modules
just gomod
