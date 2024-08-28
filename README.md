# Buildkite Agent Stack for Kubernetes

[![Build status](https://badge.buildkite.com/d58c90abfe8b48f8d8750dac8e911fc0b6afe026631b4dc97c.svg?branch=main)](https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack)

## Table of Contents
- [Overview](#Overview)
- [How does it work](#How-does-it-work)
- [Architecture](#Architecture)
- [Installation](#Installation)
  - [Requirements](#Requirements)
  - [Deploy with Helm](#Deploy-with-Helm)
  - [Options](#Options)
- [Sample Buildkite Pipeline](#Sample-Buildkite-Pipelines)
  - [Cloning repos via SSH](#Cloning-repos-via-SSH)
  - [Cloning repos via HTTPS](#Cloning-repos-via-HTTPS)
  - [Pod Spec Patch](#Pod-Spec-Patch)
  - [Sidecars](#Sidecars)
  - [Extra volume mounts](#Extra-volume-mounts)
  - [Skipping checkout](#Skipping-checkout)
  - [Overriding flags for git clone/fetch](#Overriding-flags-for-git-clonefetch)
  - [Validating your pipeline](#Validating-your-pipeline)
- [Securing the stack](#securing-the-stack)
  - [Prohibiting the kubernetes plugin (v0.13.0 and later)](#prohibiting-the-kubernetes-plugin-v0130-and-later)
- [How to setup agent hooks](#How-to-setup-agent-hooks)
- [Debugging](#Debugging)
- [Open Questions](#Open-Questions)

## Overview

A Kubernetes controller that runs [Buildkite steps](https://buildkite.com/docs/pipelines/defining-steps) as [Kubernetes jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/).

## How does it work

The controller uses the [Buildkite GraphQL API](https://buildkite.com/docs/apis/graphql-api) to watch for scheduled work that uses the `kubernetes` plugin.

When a job is available, the controller will create a pod to acquire and run the job. It converts the [PodSpec](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.25/#podspec-v1-core) in the `kubernetes` plugin into a pod by:

- adding an init container to:
  - copy the agent binary onto the workspace volume
  - check that other container images pull successfully before starting
- adding a container to run the buildkite agent
- adding a container to clone the source repository
- modifying the user-specified containers to:
  - overwrite the entrypoint to the agent binary
  - run with the working directory set to the workspace

The entrypoint rewriting and ordering logic is heavily inspired by [the approach used in Tekton](https://github.com/tektoncd/pipeline/blob/933e4f667c19eaf0a18a19557f434dbabe20d063/docs/developers/README.md#entrypoint-rewriting-and-step-ordering).

## Architecture

```mermaid
sequenceDiagram
    participant bc as buildkite controller
    participant gql as Buildkite GraphQL API
    participant bapi as Buildkite API
    participant kubernetes
    bc->>gql: Get scheduled builds & jobs
    gql-->>bc: {build: jobs: [{uuid: "abc"}]}
    kubernetes->>pod: start
    bc->>kubernetes: watch for pod completions
    bc->>kubernetes: create pod with agent sidecar
    kubernetes->>pod: create
    pod->>bapi: agent accepts & starts job
    pod->>pod: run sidecars
    pod->>pod: agent bootstrap
    pod->>pod: run user pods to completion
    pod->>bapi: upload artifacts, exit code
    pod->>pod: agent exit
    kubernetes->>bc: pod completion event
    bc->>kubernetes: cleanup finished pods
```

## Installation

### Requirements

- A Kubernetes cluster
- An API token with the [GraphQL scope enabled](https://buildkite.com/docs/apis/graphql-api#authentication)
- An [agent token](https://buildkite.com/docs/agent/v3/tokens)

### Deploy with Helm

The simplest way to get up and running is by deploying our [Helm](https://helm.sh) chart:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --create-namespace \
    --namespace buildkite \
    --set config.org=<your Buildkite org slug> \
    --set agentToken=<your Buildkite agent token> \
    --set graphqlToken=<your Buildkite GraphQL-enabled API token>
```

If you are using [Buildkite Clusters](https://buildkite.com/docs/agent/clusters) to isolate sets of pipelines from each other, you will need to specify the cluster's UUID in the configuration for the controller. This may be done using a flag on the `helm` command like so: `--set config.cluster-uuid=<your cluster's UUID>`, or an entry in a values file.
```yaml
# values.yaml
config:
  cluster-uuid: beefcafe-abbe-baba-abba-deedcedecade
```
The cluster's UUID may be obtained by navigating to the [clusters page](https://buildkite.com/organizations/-/clusters), clicking on the relevant cluster and then clicking on "Settings". It will be in a section titled "GraphQL API Integration".

> [!NOTE]
> Don't confuse the Cluster UUID with the UUID for the Queue. See [the docs](https://buildkite.com/docs/clusters/overview) for an explanation.

We're using Helm's support for [OCI-based registries](https://helm.sh/docs/topics/registries/),
which means you'll need Helm version 3.8.0 or newer.

This will create an agent-stack-k8s installation that will listen to the `kubernetes` queue.
See the `--tags` [option](#Options) for specifying a different queue.

### Options

```text
Usage:
  agent-stack-k8s [flags]
  agent-stack-k8s [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  lint        A tool for linting Buildkite pipelines
  version     Prints the version

Flags:
      --agent-token-secret string                  name of the Buildkite agent token secret (default "buildkite-agent-token")
      --buildkite-token string                     Buildkite API token with GraphQL scopes
      --cluster-uuid string                        UUID of the Buildkite Cluster. The agent token must be for the Buildkite Cluster.
  -f, --config string                              config file path
      --debug                                      debug logs
  -h, --help                                       help for agent-stack-k8s
      --image string                               The image to use for the Buildkite agent (default "ghcr.io/buildkite/agent:3.78.0")
      --image-pull-backoff-grace-period duration   Duration after starting a pod that the controller will wait before considering cancelling a job due to ImagePullBackOff (e.g. when the podSpec specifies container images that cannot be pulled) (default 30s)
      --job-ttl duration                           time to retain kubernetes jobs after completion (default 10m0s)
      --max-in-flight int                          max jobs in flight, 0 means no max (default 25)
      --namespace string                           kubernetes namespace to create resources in (default "default")
      --org string                                 Buildkite organization name to watch
      --poll-interval duration                     time to wait between polling for new jobs (minimum 1s); note that increasing this causes jobs to be slower to start (default 1s)
      --profiler-address string                    Bind address to expose the pprof profiler (e.g. localhost:6060)
      --prohibit-kubernetes-plugin                 Causes the controller to prohibit the kubernetes plugin specified within jobs (pipeline YAML) - enabling this causes jobs with a kubernetes plugin to fail, preventing the pipeline YAML from having any influence over the podSpec
      --tags strings                               A comma-separated list of agent tags. The "queue" tag must be unique (e.g. "queue=kubernetes,os=linux") (default [queue=kubernetes])

Use "agent-stack-k8s [command] --help" for more information about a command.
```

Configuration can also be provided by a config file (`--config` or `CONFIG`), or environment variables. In the [examples](examples) folder there is a sample [YAML config](examples/config.yaml) and a sample [dotenv config](examples/config.env).

#### Externalize Secrets

You can also have an external provider create a secret for you in the namespace before deploying the chart with helm. If the secret is pre-provisioned, replace the `agentToken` and `graphqlToken` arguments with:

```bash
--set agentStackSecret=<secret-name>
```

The format of the required secret can be found in [this file](./charts/agent-stack-k8s/templates/secrets.yaml.tpl).

#### Other Installation Methods

You can also use this chart as a dependency:

```yaml
dependencies:
- name: agent-stack-k8s
  version: "0.5.0"
  repository: "oci://ghcr.io/buildkite/helm"
```

or use it as a template:

```
helm template oci://ghcr.io/buildkite/helm/agent-stack-k8s -f my-values.yaml
```

Available versions and their digests can be found on [the releases page](https://github.com/buildkite/agent-stack-k8s/releases).

## Sample Buildkite Pipelines

For simple commands, you merely have to target the queue you configured agent-stack-k8s with.
```yaml
steps:
- label: Hello World!
  command: echo Hello World!
  agents:
    queue: kubernetes
```
For more complicated steps, you have access to the [`PodSpec`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#podspec-v1-core) Kubernetes API resource that will be used in a Kubernetes [`Job`](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#job-v1-batch).
For now, this is nested under a `kubernetes` plugin.
But unlike other Buildkite plugins, there is no corresponding plugin repository.
Rather, this is syntax that is interpreted by the `agent-stack-k8s` controller.
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

### PodSpec command and args interpretation

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

### Cloning repos via SSH

To use SSH to clone your repos, you'll need to add a secret reference via an [EnvFrom](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.25/#envfromsource-v1-core) to your pipeline to specify where to mount your SSH private key from.
Place this object under a `gitEnvFrom` key in the `kubernetes` plugin (see the example below).

You should create a secret in your namespace with an environment variable name that's recognised by [`docker-ssh-env-config`](https://github.com/buildkite/docker-ssh-env-config).
A script from this project is included in the default entrypoint of the default [`buildkite/agent`](https://hub.docker.com/r/buildkite/agent) Docker image.
It will process the value of the secret and write out a private key to the `~/.ssh` directory of the checkout container.

However this key will not be available in your job containers.
If you need to use git ssh credentials in your job containers, we recommend one of the following options:
1. Use a container image that's based on the default `buildkite/agent` docker image and preserve the default entrypoint by not overriding the command in the job spec.
2. Include or reproduce the functionality of the [`ssh-env-config.sh`](https://github.com/buildkite/docker-ssh-env-config/blob/-/ssh-env-config.sh) script in the entrypoint for your job container image

#### Example secret creation for ssh cloning
You most likely want to use a more secure method of managing k8s secrets. This example is illustrative only.

Supposing a SSH private key has been created and its public key has been registered with the remote repository provider (e.g. [GitHub](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/adding-a-new-ssh-key-to-your-github-account)).
```bash
kubectl create secret generic my-git-ssh-credentials --from-file=SSH_PRIVATE_DSA_KEY="$HOME/.ssh/id_ecdsa"
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

### Cloning repos via HTTPS

To use HTTPS to clone private repos, you can use a `.git-credentials` file stored in a secret, and
refer to this secret using the `gitCredentialsSecret` checkout parameter.

By default, this secret is only attached, and Git is only configured to use it, within the
`checkout` container. It will not necessarily be available in your job containers.
If you need the `.git-credentials` file inside the other containers as well, you can add a volume
mount for the `git-credentials` volume, and configure Git to use the file within it (e.g. with
`git config credential.helper 'store --file ...'`)

#### Example secret creation for HTTPS cloning
Once again, this example is illustrative only.

First, create a Kubernetes secret containing the key `.git-credentials`, formatted in the manner
expected by [the `store` Git credendial helper](https://git-scm.com/docs/git-credential-store):

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

### Pod Spec Patch
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

#### Custom Images
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

#### Default Resources
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

### Sidecars

Sidecar containers can be added to your job by specifying them under the top-level `sidecars` key. See [this example](internal/integration/fixtures/sidecars.yaml) for a simple job that runs `nginx` as a sidecar, and accesses the nginx server from the main job.

There is no guarantee that your sidecars will have started before your job, so using retries or a tool like [wait-for-it](https://github.com/vishnubob/wait-for-it) is a good idea to avoid flaky tests.

### Extra volume mounts

In some situations, for example if you want to use [git mirrors](https://buildkite.com/docs/agent/v3#promoted-experiments-git-mirrors) you may want to attach extra volume mounts (in addition to the `/workspace` one) in all the pod containers.

See [this example](internal/integration/fixtures/extra-volume-mounts.yaml), that will declare a new volume in the `podSpec` and mount it in all the containers. The benefit, is to have the same mounted path in all containers, including the `checkout` container.

### Skipping checkout

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

### Overriding flags for git clone/fetch

`git clone` and `git fetch` flags can be overridden per-step (similar to `BUILDKITE_GIT_CLONE_FLAGS` and `BUILDLKITE_GIT_FETCH_FLAGS` env vars) with the `checkout` block also:

```yaml
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

### Validating your pipeline

With the unstructured nature of Buildkite plugin specs, it can be frustratingly
easy to mess up your configuration and then have to debug why your agent pods are failing to start.
To help prevent this sort of error, there's a linter that uses [JSON
schema](https://json-schema.org/) to validate the pipeline and plugin
configuration.

This currently can't prevent every sort of error, you might still have a reference to a Kubernetes volume that doesn't exist, or other errors of that sort, but it will validate that the fields match the API spec we expect.

Our JSON schema can also be used with editors that support JSON Schema by configuring your editor to validate against the schema found [here](./cmd/linter/schema.json).

## Securing the stack

### Prohibiting the kubernetes plugin (v0.13.0 and later)

Suppose you want to enforce the podSpec used for all jobs at the controller level, and prevent users from setting or overriding that podSpec (or various other parameters) through use of the kubernetes plugin.
This can be achieved with `prohibit-kubernetes-plugin`, either as a controller flag or within the config `values.yaml`:

```yaml
# values.yaml
...
config:
  prohibit-kubernetes-plugin: true
  pod-spec-patch:
    # Override the default podSpec here.
  ...
```

With `prohibit-kubernetes-plugin` enabled, any job containing the kubernetes plugin will fail.

## How to setup agent hooks

This section explains how to setup agent hooks when running Agent Stack K8s. In order for the agent hooks to work, they must be present on the instances where the agent runs.

In case of agent-stack-k8s, we need these hooks to be accessible to the kubernetes pod where the `checkout` and `command` containers will be running. Best way to make this happen is to create a configmap with the agent hooks and mount the configmap as volume to the containers.

Here is the command to create `configmap` which will have agent hooks in it:

```
kubectl create configmap buildkite-agent-hooks --from-file=/tmp/hooks -n buildkite
```
We have all the hooks under directory `/tmp/hooks` and we are creating `configmap` with name `buildkite-agent-hooks` in `buildkite`
namespace in the k8s cluster.

Here is how to make these hooks in configmap available to the containers. Here is the pipeline config for setting up agent hooks:

```
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

   ```
          env:
          - name: BUILDKITE_HOOKS_PATH
            value: /buildkite/hooks
   ```

2. Define `VolumeMounts` using `extraVolumeMounts` which will be the path where the hooks will be mounted to with in the containers

   ```
         extraVolumeMounts:
        - mountPath: /buildkite/hooks
          name: agent-hooks
   ```

3. Define `volumes` where the configmap will be mounted

   ```
           volumes:
          - configMap:
              defaultMode: 493
              name: buildkite-agent-hooks
            name: agent-hooks
   ```
   Note: Here defaultMode `493` is setting the Unix permissions to `755` which enables the hooks to be executable. Also another way to make this hooks directory available to containers is to use [hostPath](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath)
   mount but it is not a recommended approach for production environments.

Now when we run this pipeline agent hooks will be available to the container and will run them.

Key difference we will notice with hooks execution with `agent-stack-k8s` is that environment hooks will execute twice, but checkout-related hooks such as `pre-checkout`, `checkout` and `post-checkout`
will only be executed once in the `checkout` container. Similarly the command-related hooks like `pre-command`, `command` and `post-command` hooks will be executed once by the `command` container(s).

If the env `BUILDKITE_HOOKS_PATH` is set at pipeline level instead of container like shown in above pipeline config then hooks will run for both `checkout` container and `command` container(s).

Here is the pipeline config where env `BUILDKITE_HOOKS_PATH` is exposed to all containers in the pipeline:

```
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

This is because agent-hooks will be present in both containers and `environment` hook will run in both containers. Here is how the build output will look like:

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

```
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

## Debugging
Use the `log-collector` script in the `utils` folder to collect logs for agent-stack-k8s.

### Prerequisites

- kubectl binary
- kubectl setup and authenticated to correct k8s cluster

### Inputs to the script

k8s namespace where you deployed agent stack k8s and where you expect their k8s jobs to run.

Buildkite job id for which you saw issues.

### Data/logs gathered:

The script will collect kubectl describe of k8s job, pod and agent stack k8s controller pod.

It will also capture kubectl logs of k8s pod for the Buildkite job, agent stack k8s controller pod and package them in a
tar archive which you can send via email to support@buildkite.com.

## Open questions

- How to deal with stuck jobs? Timeouts?
- How to deal with pod failures (not job failures)?
  - Report failure to buildkite from controller?
  - Emit pod logs to buildkite? If agent isn't starting correctly
  - Retry?
