# Running Buildkite builds

* [Pipeline YAML](#pipeline-yaml)
  + [Defining Steps](#defining-steps)
  + [`kubernetes` Plugin](#kubernetes-plugin)
* [Cloning (Private) Repos](#cloning-private-repos)

## Pipeline YAML

After your `agent-stack-k8s` controller has been configured, deployed and is monitoring the GraphQL API for jobs assigned to the `kubernetes` queue, you can create builds in your pipelines.

### Defining steps

The simplest pipeline step can target the `kubernetes` queue with [agent tags](https://buildkite.com/docs/agent/v3/queues):

```yaml
steps:
- label: :kubernetes: Hello World!
  command: echo Hello World!
  agents:
    queue: kubernetes
```

This will create a Buildkite job containing an agent tag of `queue=kubernetes`.
The `agent-stack-k8s` controller will retrieve this job via the GraphQL API and convert it into a Kubernetes Job.
The Kubernetes Job will contain a single Pod, with containers that will checkout the pipeline's Git repository and use the (default image) `buildkite/agent:latest` container to run the `echo Hello World!` command.

### `kubernetes` plugin

Additional configuration can use the `kubernetes` plugin to define more complicated pipeline steps.
Unlike other Buildkite plugins, there is no corresponding plugin repository for the `kubernetes` plugin.
Rather, this is reserved syntax that is interpreted by the `agent-stack-k8s` controller.
For example, defining `checkout.skip: true` will skip cloning the pipeline's repo for the job:

```yaml
steps:
- label: :kubernetes: Hello World!
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

Configuring Git credentials can be found under [Git credentials](git_credentials.md).
