# Running Buildkite builds

* [Pipeline YAML](#pipeline-yaml)
* [Cloning (private) repositories](#cloning-private-repositories)
* [Kubernetes Node Selection](#kubernetes-node-selection)

## Pipeline YAML

After your `agent-stack-k8s` controller has been configured, deployed and is monitoring the Agent API for jobs assigned to the `kubernetes` queue, you can create builds in your pipelines.

### Defining steps

The simplest pipeline step can target the `kubernetes` queue with [agent tags](https://buildkite.com/docs/agent/v3/queues):

```yaml
steps:
- label: ":kubernetes: Hello World!"
  command: echo Hello World!
  agents:
    queue: kubernetes
```

This will create a Buildkite job containing an agent tag of `queue=kubernetes`.
The `agent-stack-k8s` controller will retrieve this job via the Agent API and convert it into a Kubernetes Job.
The Kubernetes Job will contain a single Pod, with containers that will checkout the pipeline's Git repository and use the (default image) `buildkite/agent:latest` container to run the `echo Hello World!` command.

### `kubernetes` plugin

Additional configuration can use the `kubernetes` plugin to define more complicated pipeline steps.
Unlike other Buildkite plugins, there is no corresponding plugin repository for the `kubernetes` plugin.
Rather, this is reserved syntax that is interpreted by the `agent-stack-k8s` controller.
For example, defining `checkout.skip: true` will skip cloning the pipeline's repo for the job:

```yaml
steps:
- label: ":kubernetes: Hello World!"
  command: echo Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        skip: true
```

## Cloning (private) repositories

Just like with a standalone installation of the Buildkite Agent, in order to access and clone private repositories you will need to make Git credentials available for the Agent to use.
These credentials can be in the form of a SSH key for cloning over `ssh://` or with a `.git-credentials` file for cloning over `https://`.

Details about configuring Git credentials can be found under [Git Credentials](git_credentials.md).

## Kubernetes Node Selection

The `agent-stack-k8s` controller can schedule your Buildkite jobs to run on particular Kubernetes Nodes, using Kubernetes PodSpec fields for `nodeSelector` and `nodeName`.

### `nodeSelector`

The `agent-stack-k8s` controller can schedule your Buildkite jobs to run on particular Kubernetes Nodes with matching Labels. The [`nodeSelector`](https://kubernetes.io/docs/tasks/configure-pod-container/assign-pods-nodes/#create-a-pod-that-gets-scheduled-to-your-chosen-node) field of the PodSpec can be used to schedule your Buildkite jobs on a chosen Kubernetes Node with matching Labels.

#### Configuration values YAML file

The `nodeSelector` field can be defined in the controller's configuration via `pod-spec-patch`. This will apply to all Buildkite jobs processed by the controller:

```yaml
# values.yml
...
config:
  pod-spec-patch:
    nodeSelector:
      nodecputype: "amd64"  # <--- run on nodes labelled as 'nodecputype=amd64'
...
```

#### `kubernetes` plugin

The `nodeSelector` field can be defined in under `podSpecPatch` using the `kubernetes` plugin. It will apply only to this job and will override any __matching__ labels defined under `nodeSelector` in the controller's configuration:

```yaml
# pipeline.yaml
steps:
- label: ":kubernetes: Hello World!"
  command: echo Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      podSpecPatch:
        nodeSelector:
          nodecputype: "arm64"  # <--- override nodeSelector `nodecputype` label from 'amd64' -> 'arm64'
...
```

### `nodeName`

The [`nodeName`](https://kubernetes.io/docs/tasks/configure-pod-container/assign-pods-nodes/#create-a-pod-that-gets-scheduled-to-specific-node) field of the PodSpec can also be used to schedule your Buildkite jobs on a specific Kubernetes Node.

#### Configuration values YAML file

The `nodeName` field can be defined in the controller's configuration via `pod-spec-patch`. This will apply to all Buildkite jobs processed by the controller:

```yaml
# values.yml
...
config:
  pod-spec-patch:
    nodeName: "k8s-worker-01"
...
```

#### `kubernetes` plugin

The `nodeName` field can be defined in under `podSpecPatch` using the `kubernetes` plugin. It will apply only to this job and will override `nodeName` in the controller's configuration:

```yaml
# pipeline.yaml
steps:
- label: ":kubernetes: Hello World!"
  command: echo Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      podSpecPatch:
        nodeName: "k8s-worker-03"  # <--- override nodeName 'k8s-worker-01' -> 'k8s-worker-03'
...
```
