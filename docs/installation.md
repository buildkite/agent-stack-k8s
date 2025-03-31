# Installation

- [Requirements](#requirements)
- [Deploy with Helm](#deploy-with-helm)
   * [Inline Configuration](#inline-configuration)
   * [Configuration Values YAML File](#configuration-values-yaml-file)
   * [How to Find a Buildkite Cluster's UUID](#how-to-find-a-buildkite-clusters-uuid)
   * [Store Buildkite Tokens in Kubernetes Secret](#store-buildkite-tokens-in-kubernetes-secret)
      + [Convert both values to base64:](#convert-both-values-to-base64)
      + [Run the following command to create a Kubernetes Secret containing the base64 encoded Tokens:](#run-the-following-command-to-create-a-kubernetes-secret-containing-the-base64-encoded-tokens)
      + [Configure Controller to Use Kubernetes Secret](#configure-controller-to-use-kubernetes-secret)
- [Other Installation Methods](#other-installation-methods)
- [Controller Configuration](#controller-configuration)
- [Running Builds](#running-builds)


## Requirements

- A Kubernetes cluster
- A Buildkite API Access Token with the [GraphQL scope enabled](https://buildkite.com/docs/apis/graphql-api#authentication)
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
    --set graphqlToken=<Buildkite GraphQL-enabled API Access Token> \
    --set config.org=<Buildkite Org Slug> \
    --set config.cluster-uuid=<Buildkite Cluster UUID> \
--set config.tags="{queue=kubernetes}"
```

### Configuration values YAML file

Create a YAML file with the following values:

```yaml
# values.yml
agentToken: <Buildkite Cluster Agent Token>
graphqlToken: <Buildkite GraphQL-enabled API Access Token>
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
- The controller will use the provided `graphqlToken` to query the Buildkite GraphQL API looking for jobs:
  - In your Organization (`config.org`)
  - Assigned to the `kubernetes` Queue in your Cluster (`config.cluster-uuid`)

### How to find a Buildkite Cluster's UUID

To find the UUID of a Cluster:
- Go to the [Clusters page](https://buildkite.com/organizations/-/clusters)
- Click on the Cluster containing your Cluster Queue
- Click on "Settings"
- The UUID of the Cluster UUID will shown under "GraphQL API Integration"

### Store Buildkite tokens in Kubernetes Secret

If you would prefer to store your Agent Token and GraphQL API Access Token as a Kubernetes Secret to be referenced by the `agent-stack-k8s` controller:

#### Convert both values to base64:

```
echo -n <Buildkite Cluster Agent Token> | base64
echo -n <Buildkite GraphQL-enabled API Access Token> | base64
```

#### Run the following command to create a Kubernetes Secret containing the base64 encoded Tokens:

```
kubectl create secret generic <secret-name> \
  --from-literal=BUILDKITE_AGENT_TOKEN=<base64 encoded Buildkite Cluster Agent Token> \
  --from-literal=BUILDKITE_TOKEN=<base64 encoded Buildkite GraphQL-enabled API Access Token>
```

#### Configure Controller to Use Kubernetes Secret
This Kubernetes Secret name can be provided to the controller with the `agentStackSecret` option, replacing both `agentToken` and `graphqlToken` options. You can then reference your Kubernetes Secret by name during Helm chart deployments with inline configuration:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --namespace buildkite \
    --create-namespace \
    --set agentStackSecret=<Kubernetes Secret name> \
    --set config.org=<Buildkite Org Slug> \
    --set config.cluster-uuid=<Buildkite Cluster UUID> \
    --tags queue=kubernetes
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
  version: "0.26.3"
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

After the `agent-stack-k8s` Kubernetes controller has been configured and deployed, you are ready to [run Buildkite jobs](running_builds.md).
