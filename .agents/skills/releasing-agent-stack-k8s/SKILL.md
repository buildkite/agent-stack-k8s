---
name: releasing-agent-stack-k8s
description: Prepares reviewed version-bump PRs and release notes for agent-stack-k8s, then verifies automated Buildkite publication. Use when asked to release this repository.
---

# Releasing agent-stack-k8s

Release through a reviewed PR, not a manually pushed tag. Read `DEVELOPMENT.md`'s release procedure and `.buildkite/pipeline.yaml` before starting; they define the current automation.

## Prepare the release

1. Fetch `origin/main` and tags, inspect the worktree, and list GitHub releases, including drafts. Preserve unrelated work. Identify the last published stable release and inspect every change since its tag, including relevant PR descriptions and compatibility/security implications.
2. Recommend a SemVer bump from the actual changes. Use the user's requested version when supplied, unless it conflicts with an existing release. Don't release a maintenance version from a branch containing new features without explaining the conflict.
3. Create a branch such as `release-0.52.0` from `origin/main`. Change only `VERSION` to the bare version, such as `0.52.0`. The pipeline supplies the chart and application versions; don't bump `Chart.yaml` or create a changelog file.
4. Draft the final release notes as the PR body. Link the previous-to-new tag comparison and relevant PRs. Group changes into `Added`, `Fixed`, `Changed`, `Security` and `Internal`, omitting empty sections. Explain upgrade requirements and security limitations. Don't invent image digests; CI appends the image details.
5. Commit the version bump separately from any release tooling or skill changes. Before opening the PR, verify the diff, the previous release, the version and the notes against the included changes.
6. When authorised to push and create a PR, open it against `main`, title it `release: v0.52.0`, and apply the `release` label. If the label is missing, ask before creating shared repository metadata. Use the repository's normal review process; don't bypass required approvals. Review the body as carefully as the version diff. Refresh the notes if other changes land before merge.

## Publish and verify

- Merging the release PR authorises publication. After main's checks pass, Buildkite validates the version bump, merged PR, label and non-empty body before publishing versioned controller images or charts. Ordinary main commits only publish development artifacts.
- After deployment passes, Buildkite builds archives and checksums locally, advances registry aliases, then runs `gh release create --target <merge-commit>` with the PR body plus generated image details. GitHub creates the tag; don't create or push one yourself. Keep the label and final body unchanged until publication completes; the body is read at publication time.
- Watch the main build at `https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack`. Verify the GitHub release is published, points to the expected commit, contains the reviewed notes and image digests, and has the Linux amd64/arm64 archives and checksums. Verify published checksums if downloading assets.
- On failure, inspect the failed job and existing release and registry state before retrying. Already published versions and older versions are rejected. A failed GitHub upload can leave a partially populated release requiring explicit repair. Don't delete or move tags, overwrite published releases, rerun deployments or publish artifacts manually without explicit approval.
- Report the actual delivery state and any remaining review or CI gate. Don't call a release complete just because the version PR exists.

## Boundaries

- Only stable versions are supported by this pipeline. Prereleases need a separate design for registry aliases and GitHub release flags.
- GitHub creates lightweight, unsigned tags when releases are created. Tag builds are skipped by the new pipeline. Historical commits contain the old tag-driven pipeline; repository tag rules are needed to prevent that separate bypass.
- GitHub PR bodies are editable after review. This process uses the current body, like the agent release process, rather than an immutable changelog in the commit.
- Existing main-branch review requirements supply the approval gate. Don't weaken repository protections or use an administrative bypass to merge a release.
