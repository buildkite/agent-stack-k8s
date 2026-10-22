#!/usr/bin/env bash

set -euo pipefail

if [[ "${BUILDKITE_BRANCH:-}" != main || -n "${BUILDKITE_TAG:-}" || "${BUILDKITE_PULL_REQUEST:-false}" != false ]]; then
    echo "Releases can only be prepared by a main branch build, not tags or PRs" >&2
    exit 1
fi

# Introducing VERSION bootstraps the process; it doesn't release the old version.
git rev-parse --verify HEAD^ >/dev/null
if ! previous="$(git show HEAD^:VERSION 2>/dev/null)"; then
    echo "Initial VERSION; nothing to release"
    exit 0
fi

version="$(cat VERSION)"
if [[ "$previous" == "$version" ]]; then
    echo "VERSION hasn't changed; nothing to release"
    exit 0
fi

bash .buildkite/steps/release-notes.sh >/dev/null
buildkite-agent meta-data set release-version "$version"
