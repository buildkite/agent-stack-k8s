#!/usr/bin/env ash

set -eufo pipefail

echo --- :hammer: Installing tools
apk add --update-cache --no-progress crane git

echo --- :git::docker: determining version and tags
source .buildkite/steps/repo_info.sh

echo --- :doker: Logging into ghcr.io
crane auth login ghcr.io \
  --username "$REGISTRY_USERNAME" \
  --password "$REGISTRY_PASSWORD"

echo --- :crane: tagging images latest on ghcr.io
crane tag "$controller_image" latest
crane tag "$agent_image" latest
crane tag "${helm_image#oci://}" latest
