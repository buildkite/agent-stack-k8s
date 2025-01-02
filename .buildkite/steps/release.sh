#!/usr/bin/env ash

set -eufo pipefail

if [[ -z "${BUILDKITE_TAG:-}" ]]; then
  echo "^^^ +++"
  echo "This step should only be run on a tag" >&2
  exit 1
fi

ARCH="$(uname -m)"
GORELEASER_VERSION="1.19.2"
GORELEASER_URL="https://github.com/goreleaser/goreleaser/releases/download"
GORELEASER_FILE="goreleaser_${GORELEASER_VERSION}_${ARCH}.apk"
GHCH_VERSION="0.11.0"
GHCH_URL="https://github.com/buildkite/ghch/releases/download/v${GHCH_VERSION}/ghch-$(go env GOARCH)"

echo --- :hammer: Installing packages
apk add --no-progress aws-cli crane git jq
wget -q "${GORELEASER_URL}/v${GORELEASER_VERSION}/${GORELEASER_FILE}"
apk add --no-progress --allow-untrusted "${GORELEASER_FILE}"
rm "${GORELEASER_FILE}"
wget -qO- "${GHCH_URL}" > /usr/bin/ghch
chmod +x /usr/bin/ghch

echo --- :git: Determining release version from tags
# ensure we remove leading `v`
version="${BUILDKITE_TAG#v}"
# put it back
tag="v${version}"
previous_tag="$(git describe --abbrev=0 --exclude "${tag}")"
echo "Version: ${version}"
echo "Tag: ${tag}"
echo "Previous tag: ${previous_tag}"

agent_version="$(buildkite-agent meta-data get agent-version)"
echo "Agent version: ${agent_version}"

source .buildkite/steps/assume-role.sh
echo --- Logging into Public ECR
crane auth login public.ecr.aws \
  --username AWS \
  --password "$(aws --region us-east-1 ecr-public get-login-password)"

echo --- :docker: Logging into ghcr.io
crane auth login ghcr.io \
  --username "${REGISTRY_USERNAME}" \
  --password "${REGISTRY_PASSWORD}"

echo --- :docker: Tagging latest images
crane tag "public.ecr.aws/buildkite/helm/agent-stack-k8s:${version}" latest
crane tag "public.ecr.aws/buildkite/agent-stack-k8s/controller:${version}" latest
crane tag "ghcr.io/buildkite/helm/agent-stack-k8s:${version}" latest
crane tag "ghcr.io/buildkite/agent-stack-k8s/controller:${version}" latest

echo --- :golang: Creating draft release with goreleaser
chart_digest="$(crane digest "public.ecr.aws/buildkite/helm/agent-stack-k8s:$version")"
controller_digest="$(crane digest "public.ecr.aws/buildkite/agent-stack-k8s/controller:$version")"
agent_digest="$(crane digest "ghcr.io/buildkite/agent:${agent_version}")"

# TODO: remove once world write issues is fixed
git stash -uk

changelog="$(ghch --format=markdown --from="$previous_tag" --next-version="$tag")"
footer="
## Images
### Helm chart
Image: \`public.ecr.aws/buildkite/helm/agent-stack-k8s:${version}\`
Image: \`ghcr.io/buildkite/helm/agent-stack-k8s:${version}\`
Digest: \`${chart_digest}\`

### Controller
Image: \`public.ecr.aws/buildkite/agent-stack-k8s/controller:${version}\`
Image: \`ghcr.io/buildkite/agent-stack-k8s/controller:${version}\`
Digest: \`${controller_digest}\`

### Agent
Image: \`ghcr.io/buildkite/agent:${agent_version}\`
Digest: \`${agent_digest}\`
"

goreleaser release --clean --release-notes <(printf "%s\n\n\n%s" "${changelog}" "${footer}")
