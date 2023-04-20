#!/usr/bin/env sh

set -euf

echo --- :hammer: Installing tools
apk add --update-cache --no-progress git

echo --- :golang: Generating code
go generate ./...

echo --- :git: Checking generated code matches commit
# TODO: remove `-G.` once chmod 777 issue is fixed
if ! git diff -G. --no-ext-diff --exit-code; then
  echo +++ :x: Generated code was not commited.
  echo "Run"
  echo "  go generate ./..."
  echo "and make a commit."

  exit 1
fi
