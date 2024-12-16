#!/usr/bin/env ash

set -eufo pipefail

echo --- :hammer: Installing tools
apk add --update-cache --no-progress helm git

source .buildkite/steps/repo_info.sh

echo --- :helm: Helm upgrade
helm upgrade agent-stack-k8s "${helm_repo_pecr}/agent-stack-k8s" \
  --version "${version}" \
  --namespace buildkite \
  --install \
  --create-namespace \
  --wait \
  --set config.org="${BUILDKITE_ORGANIZATION_SLUG}" \
  --set agentToken="${BUILDKITE_AGENT_TOKEN}" \
  --set graphqlToken="${BUILDKITE_TOKEN}" \
  --set config.image="${agent_image}" \
  --set config.debug=true \
  --set config.profiler-address=localhost:6060
