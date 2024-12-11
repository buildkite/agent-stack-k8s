# vim: ft=sh
# shellcheck disable=SC2034  # this file will be sourced

set -eufo pipefail

tag="$(git describe)"
version="${tag#v}"
agent_image="$(buildkite-agent meta-data get agent-image)"

# Is this a release version (version-tagged)?
if [[ "${version}" == "${BUILDKITE_TAG#v}" ]] ; then
    # Publish releases to both PECR and GHCR
    controller_repo_pecr="public.ecr.aws/buildkite/agent-stack-k8s/controller"
    controller_image_pecr="$(buildkite-agent meta-data get controller-image-pecr)"
    helm_repo_pecr="oci://public.ecr.aws/buildkite/helm"
    helm_image_pecr="${helm_repo_pecr}/agent-stack-k8s:${version}"

    controller_repo_ghcr="ghcr.io/buildkite/agent-stack-k8s/controller"
    controller_image_ghcr="$(buildkite-agent meta-data get controller-image-ghcr)"
    helm_repo_ghcr="oci://ghcr.io/buildkite/helm"
    helm_image_ghcr="${helm_repo_ghcr}/agent-stack-k8s:${version}"
else
    # Publish dev images to PECR dev repo only
    controller_repo_pecr="public.ecr.aws/buildkite/agent-stack-k8s-dev/controller"
    controller_image_pecr="$(buildkite-agent meta-data get controller-image-pecr)"
    helm_repo_pecr="oci://public.ecr.aws/buildkite/helm-dev"
    helm_image_pecr="${helm_repo_pecr}/agent-stack-k8s:${version}"
fi

echo "version=${version}"
echo "controller_image_pecr=${controller_image_pecr}"
echo "controller_image_ghcr=${controller_image_ghcr:-}"
echo "agent_image=${agent_image}"
echo "helm_image_pecr=${helm_image_pecr}"
echo "helm_image_ghcr=${helm_image_ghcr:-}"
