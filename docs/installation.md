# Installation

- [Requirements](#requirements)
- [Deploy with Helm](#deploy-with-helm)
   - [Inline configuration](#inline-configuration)
   - [Configuration values YAML file](#configuration-values-yaml-file)
   - [How to find a Buildkite cluster's UUID](#how-to-find-a-buildkite-clusters-uuid)
   - [Store Buildkite tokens in a Kubernetes Secret](#store-buildkite-tokens-in-kubernetes-secret)
- [Other installation methods](#other-installation-methods)
- [Controller configuration](#controller-configuration)
- [Running builds](#running-builds)

## Requirements

- A Kubernetes cluster
- A Cluster [Agent Token](https://buildkite.com/docs/agent/v3/tokens#create-a-token)
- A Cluster [Queue](https://buildkite.com/docs/pipelines/clusters/manage-queues#create-a-self-hosted-queue)
  - The UUID of this Cluster is also required. See [Obtain Cluster UUID](#how-to-find-a-buildkite-clusters-uuid)

## Deploy with Helm

You'll need [Helm](https://github.com/helm/helm) version `v3.8.0` or newer since we're using Helm's support for [OCI-based registries](https://helm.sh/docs/topics/registries/).

### Inline configuration

The simplest way to get up and running is by deploying our [Helm](https://helm.sh) chart with an inline configuration:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --namespace buildkite \
    --create-namespace \
    --set agentToken=<Buildkite Cluster Agent Token> \
    --set config.org=<Buildkite Org Slug> \
    --set config.cluster-uuid=<Buildkite Cluster UUID> \
    --set-json='config.tags=["queue=kubernetes"]'
```

### Configuration values YAML file

Create a YAML file with the following values:

```yaml
# values.yml
agentToken: <Buildkite Cluster Agent Token>
config:
  org: <Buildkite Org Slug>
  cluster-uuid: <Buildkite Cluster UUID>
  tags:
    - queue=kubernetes
```

Now deploy the Helm chart, referencing the configuration values YAML file:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --namespace buildkite \
    --create-namespace \
    --values values.yml
```

Both of these deployment methods will:
- Create a Kubernetes deployment and install the `agent-stack-k8s` controller as a Pod running in the `buildkite` Namespace
  - The `buildkite` Namespace is created if it does not already exist in the Kubernetes cluster
- The controller will use the provided `agentToken` to query the Buildkite Agent API looking for jobs:
  - In your Organization (`config.org`)
  - Assigned to the `kubernetes` Queue in your Cluster (`config.cluster-uuid`)

### How to find a Buildkite Cluster's UUID

To find the UUID of a Cluster:
- Go to the [Clusters page](https://buildkite.com/organizations/-/clusters)
- Click on the Cluster containing your Cluster Queue
- Click on "Settings"
- The UUID of the Cluster UUID will shown under "GraphQL API Integration"

### Store Buildkite tokens in Kubernetes Secret

If you would prefer to self-manage a Kubernetes Secret containing the Agent Token, instead of allowing the Helm chart to create a secret automatically, then a custom secret can be referenced by the `agent-stack-k8s` controller.
Here's how such a secret can be created:

```bash
kubectl create secret generic <secret-name> -n buildkite \
  --from-literal=BUILDKITE_AGENT_TOKEN='<Buildkite Cluster Agent Token>'
```

This Kubernetes Secret name can be provided to the controller with the `agentStackSecret` option, replacing the `agentToken` option. You can then reference your Kubernetes Secret by name during Helm chart deployments with inline configuration:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --namespace buildkite \
    --create-namespace \
    --set agentStackSecret=<Kubernetes Secret name> \
    --set config.org=<Buildkite Org Slug> \
    --set config.cluster-uuid=<Buildkite Cluster UUID> \
    --set-json='config.tags=["queue=kubernetes"]'
```

Or with your configuration values YAML file:

```yaml
# values.yml
agentStackSecret: <Kubernetes Secret name>
config:
  org: <Buildkite Org Slug>
  cluster-uuid: <Buildkite Cluster UUID>
  tags:
    - queue=kubernetes
```

## Other installation methods

You can also use this chart as a dependency:

```yaml
dependencies:
- name: agent-stack-k8s
  version: "0.28.0"
  repository: "oci://ghcr.io/buildkite/helm"
```

You can also use this chart as a Helm [template](https://helm.sh/docs/chart_best_practices/templates/):

```
helm template oci://ghcr.io/buildkite/helm/agent-stack-k8s --values values.yaml
```

Latest and previous `agent-stack-k8s` versions (with digests) can be found under [Releases](https://github.com/buildkite/agent-stack-k8s/releases).

## Controller configuration

Detailed configuration options can be found under [Controller Configuration](controller_configuration.md)

## Running builds

After the `agent-stack-k8s` Kubernetes controller has been configured and deployed, you are ready to [run a Buildkite build](running_builds.md).
