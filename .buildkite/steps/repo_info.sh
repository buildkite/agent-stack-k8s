# vim: ft=sh
# shellcheck disable=SC2034  # this file will be sourced

set -eufo pipefail

tag=$(git describe)
version=${tag#v}

if [[ "${BUILDKITE_PULL_REQUEST:false}" != "false" ]]; then
  version="${version}-PR-${BUILDKITE_PULL_REQUEST}"
fi

agent_image=$(buildkite-agent meta-data get agent-image)
controller_image=$(buildkite-agent meta-data get controller-image)
helm_repo="oci://ghcr.io/buildkite/helm"
helm_image="$helm_repo/agent-stack-k8s:$version"

echo version="$version"
echo controller_image="$controller_image"
echo agent_image="$agent_image"
echo helm_image="$helm_image"
