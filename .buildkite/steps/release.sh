#!/usr/bin/env ash

set -eufo pipefail

if [[ -z "${BUILDKITE_TAG:-}" ]]; then
  echo "^^^ +++"
  echo "This step should only be run on a tag" >&2
  exit 1
fi

ARCH=$(uname -m)
GORELEASER_VERSION=1.19.2
GORELEASER_URL=https://github.com/goreleaser/goreleaser/releases/download
GORELEASER_PATH="goreleaser_${GORELEASER_VERSION}_${ARCH}.apk"

echo --- :hammer: Installing packages
apk add --no-progress github-cli crane
wget -q "${GORELEASER_URL}/v${GORELEASER_VERSION}/${GORELEASER_PATH}"
apk add --no-progress --allow-untrusted "$GORELEASER_PATH"

echo --- :git: Determining release version from tags
# ensure we remove leading `v`
version="${BUILDKITE_TAG#v}"
# put it back
tag="v$version"

# tags for release candidate images
build_tag=$(git describe --exclude "$tag")
build_version=${build_tag#v}

tag_image() {
  if ! crane tag "$1" "$2"; then
    echo "Failed to tag image $1 with $2, maybe the build has not completed yet?" >&2
    return 1
  fi
}

echo --- :docker: Tagging images
# NB: these will fail if the commit hasn't gone through CI and produced release-candidate images yet
tag_failures=0
set +e
tag_image "ghcr.io/buildkite/helm/agent-stack-k8s:${build_version}" "$version"
((tag_failures+=$?))
tag_image "ghcr.io/buildkite/agent-stack-k8s/controller:${build_version}" "$version"
((tag_failures+=$?))
tag_image "ghcr.io/buildkite/agent-stack-k8s/agent:${build_version}" "$version"
((tag_failures+=$?))
set -e

if [[ $tag_failures != 0 ]]; then
  echo "^^^ +++"
  echo "Failed to tag images. The build on the default branch needs to" >&2
  echo "push images to the container image registry first." >&2
  echo "Aborting release." >&2
  exit 1
fi

echo --- :golang: Creating release assets with goreleaser
chart_digest=$(crane digest "ghcr.io/buildkite/helm/agent-stack-k8s:$version")
controller_digest=$(crane digest "ghcr.io/buildkite/agent-stack-k8s/controller:$version")
agent_digest=$(crane digest "ghcr.io/buildkite/agent-stack-k8s/agent:$version")

goreleaser release --rm-dist

echo -- :github: Creating draft release
gh release view "$tag" --json body -q .body >dist/body.txt

cat <<EOF >>dist/body.txt
## Images
### Helm chart
Image: \`ghcr.io/buildkite/helm/agent-stack-k8s:${version}\`
Digest: \`$chart_digest\`

### Controller
Image: \`ghcr.io/buildkite/agent-stack-k8s/controller:${version}\`
Digest: \`$controller_digest\`

### Agent
Image: \`ghcr.io/buildkite/agent-stack-k8s/agent:${version}\`
Digest: \`$agent_digest\`
EOF

gh release edit "$tag" -F dist/body.txt
