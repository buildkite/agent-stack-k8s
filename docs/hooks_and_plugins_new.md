# How to set up agent hooks and plugins (v0.16.0 and later)

The `agent-config` block within `values.yaml` accepts a `hookVolumeSource`
and `pluginVolumeSource`. If used, the corresponding volumes are named
`buildkite-hooks` and `buildkite-plugins`, and will be automatically
mounted on checkout and command containers, with the agent configured to use them.

Any volume source can be specified for agent hooks and plugins, but a common
choice is to use a `configMap`, since hooks generally aren't large and
config maps are made available across the cluster.

To create the config map containing hooks:
    ```shell
    kubectl create configmap buildkite-agent-hooks --from-file=/tmp/hooks -n buildkite
    ```

- Example of using hooks from a config map:
    ```yaml
    # values.yaml
    config:
      agent-config:
        hooksVolume:
          name: buildkite-hooks
          configMap:
            defaultMode: 493
            name: buildkite-agent-hooks
    ```

- Example of using plugins from a host path
    ([_caveat lector_](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath)):

    ```yaml
    # values.yaml
    config:
      agent-config:
        pluginsVolume:
          name: buildkite-plugins
          hostPath:
            type: Directory
            path: /etc/buildkite-agent/plugins
    ```

Note that `hooks-path` and `plugins-path` agent config options can be used to
change the mount point of the corresponding volume. The default mount points are
`/buildkite/hooks` and `/buildkite/plugins`.
