#!/usr/bin/env bash
set -euxo pipefail

if [[ ${#} -lt 1 ]]; then
    echo "Usage: ${0} [version, ie 0.1.0]" >&2
    exit 1
fi

version="${1}"
tag="v$version"

commitish=$(git describe)
git tag -f -m "$tag" "$tag"

# helm doesn't use v-prefixed versions, everything else does
# NB: these will fail if the commit hasn't gone through CI and produced release-candidate images yet
crane tag ghcr.io/buildkite/helm/agent-stack-k8s:${commitish:1} "$version"
crane tag ghcr.io/buildkite/agent-stack-k8s:${commitish} "$tag"
crane tag ghcr.io/buildkite/agent-k8s:${commitish} "$tag"

chart_digest=$(crane digest ghcr.io/buildkite/helm/agent-stack-k8s:${version})
controller_digest=$(crane digest ghcr.io/buildkite/agent-stack-k8s:${tag})
agent_digest=$(crane digest ghcr.io/buildkite/agent-k8s:${tag})

git push origin "$tag" --force
goreleaser release --rm-dist
gh release view "$tag" --json body -q .body >dist/body.txt

cat <<EOF >>dist/body.txt
## Images
### Helm chart
Image: \`ghcr.io/buildkite/helm/agent-stack-k8s:${version}\`
Digest: \`$chart_digest\`

### Controller
Image: \`ghcr.io/buildkite/agent-stack-k8s:${tag}\`
Digest: \`$controller_digest\`

### Agent
Image: \`ghcr.io/buildkite/agent-k8s:${tag}\`
Digest: \`$agent_digest\`
EOF

gh release edit "$tag" -F dist/body.txt
git checkout dist
