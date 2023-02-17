#!/usr/bin/env bash
set -euxo pipefail

if [[ ${#} -lt 1 ]]; then
    echo "Usage: ${0} [version, ie 0.1.0]" >&2
    exit 1
fi

version="${1}"
version=$(echo $version | sed 's/^v//') # strip leading 'v'
tag="v$version"

commitish=$(git describe --exclude "$tag")

tag_image() {
    exit_code=0
    crane tag $1 $2 || exit_code=$?
    if [ $exit_code -ne 0 ]; then
        echo "failed to tag image, maybe the build has not completed yet?"
        return $exit_code
    fi
}
# helm doesn't use v-prefixed versions, everything else does
# NB: these will fail if the commit hasn't gone through CI and produced release-candidate images yet
tag_image ghcr.io/buildkite/helm/agent-stack-k8s:${commitish:1} "$version"
tag_image ghcr.io/buildkite/agent-stack-k8s/controller:${commitish} "$tag"
tag_image ghcr.io/buildkite/agent-stack-k8s/agent:${commitish} "$tag"

chart_digest=$(crane digest ghcr.io/buildkite/helm/agent-stack-k8s:${version})
controller_digest=$(crane digest ghcr.io/buildkite/agent-stack-k8s/controller:${tag})
agent_digest=$(crane digest ghcr.io/buildkite/agent-stack-k8s/agent:${tag})

git tag -m "$tag" "$tag"
git push origin "$tag" --force
goreleaser release --rm-dist
gh release view "$tag" --json body -q .body >dist/body.txt

cat <<EOF >>dist/body.txt
## Images
### Helm chart
Image: \`ghcr.io/buildkite/helm/agent-stack-k8s:${version}\`
Digest: \`$chart_digest\`

### Controller
Image: \`ghcr.io/buildkite/agent-stack-k8s/controller:${tag}\`
Digest: \`$controller_digest\`

### Agent
Image: \`ghcr.io/buildkite/agent-stack-k8s/agent:${tag}\`
Digest: \`$agent_digest\`
EOF

gh release edit "$tag" -F dist/body.txt
