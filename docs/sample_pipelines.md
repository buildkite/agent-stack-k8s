# Sample Buildkite Pipelines

TOC:
    -   [PodSpec command and args interpretation](#podspec-command-and-args-interpretation)
    -   [Cloning repos via SSH](#cloning-repos-via-ssh)
    -   [Cloning repos via HTTPS](#cloning-repos-via-https)
    -   [Default job metadata](#default-job-metadata)
    -   [Pod Spec Patch](#pod-spec-patch)
    -   [Sidecars](#sidecars)
    -   [The workspace volume](#the-workspace-volume)
    -   [Extra volume mounts](#extra-volume-mounts)
    -   [Skipping checkout (v0.13.0 and later)](#skipping-checkout-v0130-and-later)
    -   [Overriding flags for git clone and git fetch (v0.13.0 and later)](#overriding-flags-for-git-clone-and-git-fetch-v0130-and-later)
    -   [Overriding other git settings (v0.16.0 and later)](#overriding-other-git-settings-v0160-and-later)
    -   [Default envFrom](#default-envfrom)


For simple commands, target the queue you configured agent-stack-k8s with. For example:

```yaml
steps:
- label: Hello World!
  command: echo Hello World!
  agents:
    queue: kubernetes
```

For more complicated steps, you have access to the [`PodSpec`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#podspec-v1-core) Kubernetes API resource that will be used in a Kubernetes [`Job`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#job-v1-batch).
Currently, this is nested under a `kubernetes` plugin - but unlike other Buildkite plugins, there is no corresponding plugin repository.
Rather, this is syntax that is interpreted by the `agent-stack-k8s` controller:

```yaml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      podSpec:
        containers:
        - image: alpine:latest
          command:
          - echo Hello World!
```

Almost any container image may be used, but it MUST have a POSIX shell available to be executed at `/bin/sh`.

## PodSpec command and args interpretation

In a `podSpec`, `command` **must** be a list of strings, since it is [defined by Kubernetes](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#entrypoint).
However, agent-stack-k8s runs buildkite-agent instead of the container's default entrypoint.
To run the command you want, it must _re-interpret_ `command` into input for buildkite-agent.
By default, it treats `command` as a sequence of multiple commands, similar to a pipeline.yaml
`steps: commands: ...`.
This is different to Kubernetes' interpretation of `command` (as an entrypoint vector run without a
shell as a single command).

This "interposer" behaviour can be changed using `commandParams/interposer`:

* `buildkite` is the default, in which agent-stack-k8s treats `command` as a sequence of multiple
  commands and `args` as extra arguments added to the end of the last command, which is then
  typically interpreted by the shell.
* `vector` emulates the Kubernetes interpretation in which `command` and `args` specify components
  of a single command intended to be run directly.
* `legacy` is the 0.14.0 and earlier behaviour in which `command` and `args` were joined directly
  into a single command with spaces.

`buildkite` example:

```yaml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      commandParams:
        interposer: buildkite  # This is the default, and can be omitted.
      podSpec:
        containers:
        - image: alpine:latest
          command:
          - set -euo pipefail
          - |-       # <-- YAML block scalars work too
            echo Hello World! > hello.txt
            cat hello.txt | buildkite-agent annotate
```

If you have a multi-line `command`, specifying the `args` as well could lead to confusion, so we
recommend just using `command`.

`vector` example:

```yaml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      commandParams:
        interposer: vector
      podSpec:
        containers:
        - image: alpine:latest
          command: ['sh']
          args:
          - '-c'
          - |-
            set -eu

            echo Hello World! > hello.txt
            cat hello.txt | buildkite-agent annotate
```

More samples can be found in the
[integration test fixtures directory](internal/integration/fixtures).

## Cloning repos via SSH

To use SSH to clone your repos, you'll need to add a secret reference via an [EnvFrom](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.25/#envfromsource-v1-core) to your pipeline to specify where to mount your SSH private key from.
Place this object under a `gitEnvFrom` key in the `kubernetes` plugin (see the example below).

You should create a secret in your namespace with an environment variable name that's recognised by [`docker-ssh-env-config`](https://github.com/buildkite/docker-ssh-env-config).
A script from this project is included in the default entrypoint of the default [`buildkite/agent`](https://hub.docker.com/r/buildkite/agent) Docker image.
It will process the value of the secret and write out a private key to the `~/.ssh` directory of the checkout container.

However, this key will not be available in your job containers.
If you need to use git ssh credentials in your job containers, we recommend one of the following options:
1. Use a container image based on the default `buildkite/agent` docker image and preserve the default entrypoint by not overriding the command in the job spec.
2. Include or reproduce the functionality of the [`ssh-env-config.sh`](https://github.com/buildkite/docker-ssh-env-config/blob/-/ssh-env-config.sh) script in the entrypoint for your job container image

### NOTE: Support for DSA keys has been removed from OpenSSH as of early 2025. This removal now affects agent version `v3.88.0` and newer. Please migrate to `RSA`, `ECDSA`, or `EDDSA` keys.

### Example secret creation for ssh cloning
You most likely want to use a more secure method of managing k8s secrets. This example is illustrative only.

If an SSH private key has been created and its public key has been registered with the remote repository provider (e.g. [GitHub](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/adding-a-new-ssh-key-to-your-github-account)).
```bash
kubectl create secret generic my-git-ssh-credentials --from-file=SSH_PRIVATE_RSA_KEY="$HOME/.ssh/id_rsa"
```

Then the following pipeline will be able to clone a git repository that requires ssh credentials.
```yaml
steps:
  - label: build image
    agents:
      queue: kubernetes
    plugins:
      - kubernetes:
          gitEnvFrom:
            - secretRef:
                name: my-git-ssh-credentials # <----
          podSpec:
            containers:
              - image: gradle:latest
                command: [gradle]
                args:
                  - jib
                  - --image=ttl.sh/example:1h
```

## Cloning repos via HTTPS

To use HTTPS to clone private repos, you can use a `.git-credentials` file stored in a secret, and
refer to this secret using the `gitCredentialsSecret` checkout parameter.

By default, this secret is only attached, and Git is only configured to use it, within the
`checkout` container. It will not necessarily be available in your job containers.
If you need the `.git-credentials` file inside the other containers as well, you can add a volume
mount for the `git-credentials` volume, and configure Git to use the file within it (e.g. with
`git config credential.helper 'store --file ...'`)

### Example secret creation for HTTPS cloning
Once again, this example is illustrative only.

First, create a Kubernetes secret containing the key `.git-credentials`, formatted in the manner
expected by [the `store` Git credential helper](https://git-scm.com/docs/git-credential-store):

```bash
kubectl create secret generic my-git-credentials --from-file='.git-credentials'="$HOME/.git-credentials"
```

Then you can use the `checkout/gitCredentialsSecret` (in your pipeline) or
`default-checkout-params/gitCredentialsSecret` (in values.yaml) to reference the secret volume
source:

```yaml
# pipeline.yaml
steps:
  - label: build image
    agents:
      queue: kubernetes
    plugins:
      - kubernetes:
          checkout:
            gitCredentialsSecret:
              secretName: my-git-credentials # <----
          podSpec:
            ...
```

```yaml
# values.yaml
...
default-checkout-params:
  gitCredentialsSecret:
    secretName: my-git-credentials
...
```

If you wish to use a different key within the secret than `.git-credentials`, you can
[project it](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/#project-secret-keys-to-specific-file-paths)
to `.git-credentials` by using `items` within `gitCredentialsSecret`.

```yaml
# values.yaml
...
default-checkout-params:
  gitCredentialsSecret:
    secretName: my-git-credentials
    items:
    - key: funky-creds
      path: .git-credentials
...
```

## Default job metadata
agent-stack-k8s can automatically add labels and annotations to the Kubernetes jobs it creates.
Default labels and annotations can be set in `values.yaml` with `default-metadata`, e.g.:

```yaml
# config.yaml / values.yaml
...
default-metadata:
  annotations:
    imageregistry: "https://hub.docker.com/"
    mycoolannotation: llamas
  labels:
    argocd.argoproj.io/tracking-id: example-id-here
    mycoollabel: alpacas
```

Similarly, they can be set for each step in a pipeline individually using the kubernetes plugin,
e.g.:

```yaml
# pipeline.yaml
...
  plugins:
    - kubernetes:
        metadata:
          annotations:
            imageregistry: "https://hub.docker.com/"
            mycoolannotation: llamas
          labels:
            argocd.argoproj.io/tracking-id: example-id-here
            mycoollabel: alpacas
```


## Pod Spec Patch
Rather than defining the entire Pod Spec in a step, there is the option to define a [strategic merge patch](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/update-api-object-kubectl-patch/) in the controller.
Agent Stack K8s will first generate a K8s Job with a PodSpec from a Buildkite Job and then apply the patch in the controller.
It will then apply the patch specified in its config file, which is derived from the value in the helm installation.
This can replace much of the functionality of some of the other fields in the plugin, like `gitEnvFrom`.

#### Eliminate `gitEnvFrom`
Here's an example demonstrating how one would eliminate the need to specify `gitEnvFrom` from every step, but still checkout private repositories.

First, deploy the helm chart with a `values.yaml` file.
```yaml
# values.yaml
agentStackSecret: <name of predefined secrets for k8s>
config:
  org: <your-org-slug>
  pod-spec-patch:
    containers:
    - name: checkout         # <---- this is needed so that the secret will only be mounted on the checkout container
      envFrom:
      - secretRef:
          name: git-checkout # <---- this is the same secret name you would have put in `gitEnvFrom` in the kubernetes plugin
```
You may use the `-f` or `--values` arguments to `helm upgrade` to specify a `values.yaml` file.
```shell
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --create-namespace \
    --namespace buildkite \
    --values values.yaml \
    --version <agent-stack-k8s version>
```

Now, with this setup, we don't even need to specify the `kubernetes` plugin to use Agent Stack K8s with a private repo
```yaml
# pipelines.yaml
agents:
  queue: kubernetes
steps:
- name: Hello World!
  commands:
  - echo -n Hello!
  - echo " World!"

- name: Hello World in one command
  command: |-
    echo -n Hello!
    echo " World!"
```

### Custom Images
You can specify a different image to use for a step in a step level `podSpecPatch`. Previously this could be done with a step level `podSpec`.
```yaml
# pipelines.yaml
agents:
  queue: kubernetes
steps:
- name: Hello World!
  commands:
  - echo -n Hello!
  - echo " World!"
  plugins:
  - kubernetes:
      podSpecPatch:
      - name: container-0
        image: alpine:latest

- name: Hello World from alpine!
  commands:
  - echo -n Hello
  - echo " from alpine!"
  plugins:
  - kubernetes:
      podSpecPatch:
      - name: container-0      # <---- You must specify this as exactly `container-0` for now.
        image: alpine:latest   #       We are experimenting with ways to make it more ergonomic
```

### Default Resources
In the helm values, you can specify default resources to be used by the containers in Pods that are launched to run Jobs.
```yaml
# values.yaml
agentStackSecret: <name of predefend secrets for k8s>
config:
  org: <your-org-slug>
  pod-spec-patch:
    initContainers:
    - name: copy-agent
    requests:
      cpu: 100m
      memory: 50Mi
    limits:
      memory: 100Mi
    containers:
    - name: agent          # this container acquires the job
      resources:
        requests:
          cpu: 100m
          memory: 50Mi
        limits:
          memory: 1Gi
    - name: checkout       # this container clones the repo
      resources:
        requests:
          cpu: 100m
          memory: 50Mi
        limits:
          memory: 1Gi
    - name: container-0    # the job runs in a container with this name by default
      resources:
        requests:
          cpu: 100m
          memory: 50Mi
        limits:
          memory: 1Gi
```
and then every job that's handled by this installation of agent-stack-k8s will default to these values. To override it for a step, use a step level `podSpecPatch`.
```yaml
# pipelines.yaml
agents:
  queue: kubernetes
steps:
- name: Hello from a container with more resources
  command: echo Hello World!
  plugins:
  - kubernetes:
      podSpecPatch:
        containers:
        - name: container-0    # <---- You must specify this as exactly `container-0` for now.
          resources:           #       We are experimenting with ways to make it more ergonomic
            requests:
              cpu: 1000m
              memory: 50Mi
            limits:
              memory: 1Gi

- name: Hello from a container with default resources
  command: echo Hello World!
```

### Overriding commands

For command containers, it is possible to alter the `command` or `args` using
PodSpecPatch. These will be re-wrapped in the necessary `buildkite-agent`
invocation.

However, PodSpecPatch will not modify the `command` or `args` values
for these containers (provided by the agent-stack-k8s controller), and will
instead return an error:

* `copy-agent`
* `imagecheck-*`
* `agent`
* `checkout`

If modifying the commands of these containers is something you want to do, first
consider other potential solutions:

* To override checkout behaviour, consider writing a `checkout` hook, or
  disabling the checkout container entirely with `checkout: skip: true`.
* To run additional containers without `buildkite-agent` in them, consider using
  a [sidecar](#sidecars).

We are continually investigating ways to make the stack more flexible while
ensuring core functionality.

> [!CAUTION]
> Avoid using PodSpecPatch to override `command` or `args` of the containers
> added by the agent-stack-k8s controller. Such modifications, if not done with
> extreme care and detailed knowledge about how agent-stack-k8s constructs
> podspecs, are very likely to break how the agent within the pod works.
>
> If the replacement command for the checkout container does not invoke
> `buildkite-agent bootstrap`:
>
>  * the container will not connect to the `agent` container, and the agent will
>    not finish the job normally because there was not an expected number of
>    other containers connecting to it
>  * logs from the container will not be visible in Buildkite
>  * hooks will not be executed automatically
>  * plugins will not be checked out or executed automatically
>
> and various other functions provided by `buildkite-agent` may not work.
>
> If the command for the `agent` container is overridden, and the replacement
> command does not invoke `buildkite-agent start`, then the job will not be
> acquired on Buildkite at all.

If you still wish to disable this precaution, and override the raw `command` or
`args` of these stack-provided containers using PodSpecPatch, you may do so with
the `allow-pod-spec-patch-unsafe-command-modification` config option.

## Sidecars

Sidecar containers can be added to your job by specifying them under the top-level `sidecars` key. See [this example](internal/integration/fixtures/sidecars.yaml) for a simple job that runs `nginx` as a sidecar, and accesses the nginx server from the main job.

There is no guarantee that your sidecars will have started before your job, so using retries or a tool like [wait-for-it](https://github.com/vishnubob/wait-for-it) is a good idea to avoid flaky tests.

## The workspace volume

By default, the workspace directory (`/workspace`) is mounted as an `emptyDir` ephemeral volume. Other volumes may be more desirable (e.g. a volume claim backed by an NVMe device).
The default workspace volume can be set as stack configuration, e.g.

```yaml
# values.yaml
config:
  workspace-volume:
    name: workspace-2-the-reckoning
    ephemeral:
      volumeClaimTemplate:
        spec:
          accessModes: ["ReadWriteOnce"]
          storageClassName: my-special-storage-class
          resources:
            requests:
              storage: 1Gi
```

## Extra volume mounts

In some situations, for example if you want to use [git mirrors](https://buildkite.com/docs/agent/v3#promoted-experiments-git-mirrors) you may want to attach extra volume mounts (in addition to the `/workspace` one) in all the pod containers.

See [this example](internal/integration/fixtures/extra-volume-mounts.yaml), that will declare a new volume in the `podSpec` and mount it in all the containers. The benefit, is to have the same mounted path in all containers, including the `checkout` container.

## Skipping checkout (v0.13.0 and later)

For some steps, you may wish to avoid checkout (cloning a source repository).
This can be done with the `checkout` block under the `kubernetes` plugin:

```yaml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        skip: true # prevents scheduling the checkout container
```

## Overriding flags for git clone and git fetch (v0.13.0 and later)

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

## Overriding other git settings (v0.16.0 and later)

From v0.16.0 onwards, many more git flags and options supported by the agent are
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

## Default envFrom

`envFrom` can be added to all checkout, command, and sidecar containers
separately, either per-step in the pipeline or for all jobs in `values.yaml`.

Pipeline example (note that the blocks are `checkout`, `commandParams`, and
`sidecarParams`):

```yaml
# pipeline.yml
...
  kubernetes:
    checkout:
      envFrom:
      - prefix: GITHUB_
        secretRef:
          name: github-secrets
    commandParams:
      interposer: vector
      envFrom:
      - prefix: DEPLOY_
        secretRef:
          name: deploy-secrets
    sidecarParams:
      envFrom:
      - prefix: LOGGING_
        configMapRef:
          name: logging-config
```

`values.yaml` example:

```yaml
# values.yml
config:
  default-checkout-params:
    envFrom:
    - prefix: GITHUB_
      secretRef:
        name: github-secrets
  default-command-params:
    interposer: vector
    envFrom:
    - prefix: DEPLOY_
      secretRef:
        name: deploy-secrets
  default-sidecar-params:
    envFrom:
    - prefix: LOGGING_
      configMapRef:
        name: logging-config
```