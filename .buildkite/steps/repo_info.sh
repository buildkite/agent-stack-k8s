# vim: ft=sh
# shellcheck disable=SC2034  # this file will be sourced

set -eufo pipefail

docker_repo_prefix=ghcr.io/buildkite
tag=$(git describe)
version=${tag#v}
temp_agent_image=$(buildkite-agent meta-data get agent-image)
agent_image="${docker_repo_prefix}/agent-stack-k8s/agent:${version}"
controller_image=$(buildkite-agent meta-data get controller-image)
helm_repo="oci://${docker_repo_prefix}/helm"
