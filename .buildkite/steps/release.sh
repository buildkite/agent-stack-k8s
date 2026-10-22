#!/usr/bin/env ash

set -euo pipefail

if [[ "${BUILDKITE_BRANCH:-}" != main || -n "${BUILDKITE_TAG:-}" || "${BUILDKITE_PULL_REQUEST:-false}" != false ]]; then
  echo "This step should only be run on main, not tags or PRs" >&2
  exit 1
fi

version="$(buildkite-agent meta-data get release-version --default '')"
if [[ -z "$version" ]]; then
  echo "No reviewed version bump; nothing to release"
  exit 0
fi

if [[ "$version" != "$(cat VERSION)" ]]; then
  echo "Release metadata doesn't match VERSION" >&2
  exit 1
fi

ARCH="$(uname -m)"
GORELEASER_VERSION="1.19.2"
GORELEASER_URL="https://github.com/goreleaser/goreleaser/releases/download"
GORELEASER_FILE="goreleaser_${GORELEASER_VERSION}_${ARCH}.apk"

echo --- :hammer: Installing packages
apk add --no-progress bash crane git github-cli jq
wget -q "${GORELEASER_URL}/v${GORELEASER_VERSION}/${GORELEASER_FILE}"
apk add --no-progress --allow-untrusted "${GORELEASER_FILE}"
rm "${GORELEASER_FILE}"

echo --- :git: Validating reviewed release notes
notes="$(mktemp)"
trap 'rm -f "$notes"' EXIT
bash .buildkite/steps/release-notes.sh > "$notes"
tag="v${version}"
echo "Version: ${version}"
echo "Tag: ${tag}"

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

echo --- :golang: Preparing release artifacts with goreleaser
chart_digest="$(crane digest "public.ecr.aws/buildkite/helm/agent-stack-k8s:$version")"
controller_digest="$(crane digest "public.ecr.aws/buildkite/agent-stack-k8s/controller:$version")"
agent_digest="$(crane digest "ghcr.io/buildkite/agent:${agent_version}")"

# TODO: remove once world write issues is fixed
git stash -uk

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

printf '\n%s\n' "$footer" >> "$notes"
# Snapshot mode builds archives/checksums with VERSION without requiring a tag
# or publishing anything. GitHub creates the tag when the release is created.
RELEASE_VERSION="$version" goreleaser release --snapshot --clean

echo --- :docker: Tagging latest images
crane tag "public.ecr.aws/buildkite/helm/agent-stack-k8s:${version}" latest
crane tag "public.ecr.aws/buildkite/agent-stack-k8s/controller:${version}" latest
crane tag "ghcr.io/buildkite/helm/agent-stack-k8s:${version}" latest
crane tag "ghcr.io/buildkite/agent-stack-k8s/controller:${version}" latest

echo --- :github: Publishing reviewed release
gh release create "$tag" dist/*.tar.gz dist/checksums.txt \
  --repo buildkite/agent-stack-k8s --target "$(git rev-parse HEAD)" \
  --title "$tag" --notes-file "$notes" --latest
