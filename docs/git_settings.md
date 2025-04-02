# Git settings

This document outlines how to override Git settings.

## Overriding flags for `git clone` and `git fetch` (v0.13.0 and later)

Flags for `git clone`, `git fetch` can be overridden per-step (similar to
`BUILDKITE_GIT_CLONE_FLAGS` and `BUILDLKITE_GIT_FETCH_FLAGS` env vars) with
the `checkout` block also:

```yaml
# pipeline.yml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        cloneFlags: -v --depth 1
        fetchFlags: -v --prune --tags
```

## Overriding other Git settings (v0.16.0 and later)

From v0.16.0 onwards, many more Git flags and options supported by the agent are
also configurable with the `checkout` block. Example:

```yaml
# pipeline.yml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        cleanFlags: -ffxdq
        noSubmodules: false
        submoduleCloneConfig: ["key=value", "something=else"]
        gitMirrors:
          path: /buildkite/git-mirrors # optional with volume
          volume:
            name: my-special-git-mirrors
            persistentVolumeClaim:
              claimName: block-pvc
          lockTimeout: 600
          skipUpdate: true
          cloneFlags: -v
```

To avoid setting `checkout` on every step, you can use `default-checkout-params`
within `values.yaml` when deploying the stack. These will apply the settings to
every job. Example:

```yaml
# values.yaml
...
config:
  default-checkout-params:
    # The available options are the same as `checkout` within `plugin.kubernetes`.
    cloneFlags: -v --depth 1
    noSubmodules: true
    gitMirrors:
      volume:
        name: host-git-mirrors
        hostPath:
          path: /var/lib/buildkite/git-mirrors
          type: Directory
```
