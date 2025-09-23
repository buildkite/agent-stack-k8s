#!/usr/bin/env ash

set -eufo pipefail

echo --- :hammer: Installing tools
apk add --update-cache --no-progress helm git

source .buildkite/steps/repo_info.sh

# Note that the Helm command below still supplies a GraphQL token,
# so that it is available in the Helm-generated Kubernetes Secret,
# so that it is available for the integration tests.

echo --- :helm: Helm upgrade
helm upgrade agent-stack-k8s "${helm_repo_pecr}/agent-stack-k8s" \
  --version "${version}" \
  --namespace buildkite \
  --install \
  --create-namespace \
  --wait \
  --set agentToken="${BUILDKITE_AGENT_TOKEN}" \
  --set config.image="${agent_image}" \
  --set config.debug=true \
  --set config.profiler-address=localhost:6060 \
  --set monitoring.podMonitor.deploy=true \
  --set monitoring.deployGrafanaDashboard=true
