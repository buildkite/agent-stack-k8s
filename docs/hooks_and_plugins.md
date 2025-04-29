# Agent Hooks and Plugins

The `agent-config` block within `values.yaml` accepts a `hookVolumeSource`
and `pluginVolumeSource`. If used, the corresponding volumes are named
`buildkite-hooks` and `buildkite-plugins`, and will be automatically
mounted on checkout and command containers, with the agent configured to use them.

## Configure agent hooks and plugins (v0.16.0 and later)

Any volume source can be specified for agent hooks and plugins, but a common
choice is to use a `configMap`, since hooks generally aren't large and
config maps are made available across the cluster.

To create the config map containing hooks:

```shell
kubectl create configmap buildkite-agent-hooks --from-file=/tmp/hooks -n buildkite
```

Using hooks from a config map:

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

Using plugins from a host path ([_caveat lector_](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath)):

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

## Configure agent hooks and plugins (v0.15.0 and earlier)

This section explains how to setup agent hooks is you are running the `agent-stack-k8s` controller `v0.15.0` and earlier. In order for the agent hooks to work, they must be present on the instances where the agent runs. These hooks need to be accessible to the Kubernetes Pod where the `checkout` and `command` containers will be running. Best way to make this happen is to create a ConfigMap with the agent hooks and mount the ConfigMap as a Volume to the containers.

Here is the command to create `configmap` which will have agent hooks in it:

```shell
kubectl create configmap buildkite-agent-hooks --from-file=/tmp/hooks -n buildkite
```

We have all the hooks under directory `/tmp/hooks` and we are creating a ConfigMap with name `buildkite-agent-hooks` in `buildkite` namespace of the K8s cluster.

Here is how to make these hooks in configmap available to the containers. Use the pipeline config for setting up agent hooks:

```yaml
# pipeline.yml
steps:
- label: ':pipeline: Pipeline Upload'
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      extraVolumeMounts:
        - mountPath: /buildkite/hooks
          name: agent-hooks
      podSpec:
        containers:
        - command:
          - echo hello-world
          image: alpine:latest
          env:
          - name: BUILDKITE_HOOKS_PATH
            value: /buildkite/hooks
        volumes:
          - configMap:
              defaultMode: 493
              name: buildkite-agent-hooks
            name: agent-hooks
```

There are 3 main aspects we need to make sure that happen for hooks to be available to the containers in `agent-stack-k8s`.

1. Define env `BUILDKITE_HOOKS_PATH` with the path `agent ` and `checkout` containers will look for hooks

   ```yaml
          env:
          - name: BUILDKITE_HOOKS_PATH
            value: /buildkite/hooks
   ```

2. Define `VolumeMounts` using `extraVolumeMounts` which will be the path where the hooks will be mounted to with in the containers

   ```yaml
        extraVolumeMounts:
        - mountPath: /buildkite/hooks
          name: agent-hooks
   ```

3. Define `volumes` where the configmap will be mounted

   ```yaml
          volumes:
          - configMap:
              defaultMode: 493
              name: buildkite-agent-hooks
            name: agent-hooks
   ```
   Note: Here defaultMode `493` is setting the Unix permissions to `755` which enables the hooks to be executable. Another way to make this hooks directory available to containers is to use [hostPath](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath)
   mount but it is not a recommended approach for production environments.

Now, when we run this pipeline, agent hooks will be available to the container and will run them.

The key difference that we will notice with hooks' execution with `agent-stack-k8s` is that environment hooks will execute twice, but checkout-related hooks such as `pre-checkout`, `checkout` and `post-checkout`
will only be executed once in the `checkout` container. Similarly, the command-related hooks like `pre-command`, `command` and `post-command` hooks will be executed once by the `command` container(s).

If the env `BUILDKITE_HOOKS_PATH` is set at pipeline level instead of container like shown in the above pipeline config then hooks will run for both `checkout` container and `command` container(s).

Here is the pipeline config where env `BUILDKITE_HOOKS_PATH` is exposed to all containers in the pipeline:

```yaml
# pipeline.yml
steps:
- label: ':pipeline: Pipeline Upload'
  env:
    BUILDKITE_HOOKS_PATH: /buildkite/hooks
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      extraVolumeMounts:
        - mountPath: /buildkite/hooks
          name: agent-hooks
      podSpec:
        containers:
        - command:
          - echo
          - hello-world
          image: alpine:latest
        volumes:
          - configMap:
              defaultMode: 493
              name: buildkite-agent-hooks
            name: agent-hooks
```

This happens because agent-hooks will be present in both containers and `environment` hook will run in both containers. Here is how the build output will look like:

```
Running global environment hook
Running global pre-checkout hook
Preparing working directory
Running global post-checkout hook
Running global environment hook
Running commands
Running global pre-exit hook
```

In scenarios where we want to `skip checkout` when running on `agent-stack-k8s`, it will cause checkout-related hooks such as pre-checkout, checkout and post-checkout not to run because `checkout` container will not be present when `skip checkout` is set.

Here is the pipeline config where checkout is skipped:

```yaml
# pipeline.yml
steps:
- label: ':pipeline: Pipeline Upload'
  env:
    BUILDKITE_HOOKS_PATH: /buildkite/hooks
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        skip: true
      extraVolumeMounts:
        - mountPath: /buildkite/hooks
          name: agent-hooks
      podSpec:
        containers:
        - command:
          - echo
          - hello-world
          image: alpine:latest
        volumes:
          - configMap:
              defaultMode: 493
              name: buildkite-agent-hooks
            name: agent-hooks
```

Now, if we look at the build output below, we can see that it only has `environment` and `pre-exit` that ran and no checkout-related hooks, unlike the earlier build output where checkout was not skipped.

```
Preparing working directory
Running global environment hook
Running commands
Running global pre-exit hook
```
