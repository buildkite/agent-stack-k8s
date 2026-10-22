#!/usr/bin/env bash

# Validate the release commit and print only its reviewed release notes.
set -euo pipefail

version="$(cat VERSION)"
stable='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
if [[ ! "$version" =~ $stable ]]; then
    echo "VERSION must contain a stable SemVer version without a v prefix" >&2
    exit 1
fi

previous="$(git show HEAD^:VERSION)"
if [[ ! "$previous" =~ $stable || "$previous" == "$version" || "$(printf '%s\n' "$previous" "$version" | sort -V | tail -1)" != "$version" ]]; then
    echo "A release commit must increase VERSION" >&2
    exit 1
fi

# Refuse duplicate publication and delayed builds of older versions.
latest="$(gh api repos/buildkite/agent-stack-k8s/releases/latest --jq .tag_name)"
if [[ "${latest#v}" == "$version" || "$(printf '%s\n' "${latest#v}" "$version" | sort -V | tail -1)" != "$version" ]]; then
    echo "Refusing to release v${version} after ${latest}" >&2
    exit 1
fi

existing="$(gh api "repos/buildkite/agent-stack-k8s/git/matching-refs/tags/v${version}" \
    --jq "map(select(.ref == \"refs/tags/v${version}\")) | length")"
if [[ "$existing" != 0 ]]; then
    echo "Tag v${version} already exists; refusing to release an existing tag" >&2
    exit 1
fi

commit="$(git rev-parse HEAD)"
gh api --paginate --slurp "repos/buildkite/agent-stack-k8s/commits/${commit}/pulls" |
    jq -er --arg commit "$commit" '
        add | map(select(
            .merged_at != null and
            .merge_commit_sha == $commit and
            .base.ref == "main" and
            .base.repo.full_name == "buildkite/agent-stack-k8s" and
            any(.labels[]; .name == "release")
        )) |
        if length == 1 and (.[0].body // "" | test("\\S")) then
            .[0].body | gsub("\r"; "")
        else
            error("Release requires a merged release-labelled PR into main with a non-empty body")
        end'
