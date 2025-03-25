# Installation

[TOC]
    -   [Requirements](#requirements)
    -   [Deploy with Helm](#deploy-with-helm)
    -   [Options](#options)
    -   [Buildkite cluster's UUID](#buildkite-clusters-uuid) 

## Requirements

- A Kubernetes cluster
- A Buildkite API Access Token with the [GraphQL scope enabled](https://buildkite.com/docs/apis/graphql-api#authentication)
- A Cluster [Agent Token](https://buildkite.com/docs/agent/v3/tokens#create-a-token)
- A Cluster [Queue](https://buildkite.com/docs/pipelines/clusters/manage-queues#create-a-self-hosted-queue)
  - The UUID of this Cluster is also required. See [Obtain Cluster UUID](#how-to-find-a-buildkite-clusters-uuid)

## Deploy with Helm

You'll need [Helm](https://github.com/helm/helm) version `v3.8.0` or newer since we're using Helm's support for [OCI-based registries](https://helm.sh/docs/topics/registries/).

### Inline Configuration

The simplest way to get up and running is by deploying our [Helm](https://helm.sh) chart with an inline configuration:
```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --namespace buildkite \
    --create-namespace \
    --set agentToken=<Buildkite Cluster Agent Token> \
    --set graphqlToken=<Buildkite GraphQL-enabled API Access Token> \
    --set config.org=<Buildkite Org Slug> \
    --set config.cluster-uuid=<Buildkite Cluster UUID> \
    --tags queue=kubernetes
```

### Configuration Values YAML File
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

### How to Find a Buildkite Cluster's UUID

To find the UUID of a Cluster:
- Go to the [Clusters page](https://buildkite.com/organizations/-/clusters)
- Click on the Cluster containing your Cluster Queue
- Click on "Settings"
- The UUID of the Cluster UUID will shown under "GraphQL API Integration"







### Controller Configuration

Detailed configuration options can be found under [Controller Configuration](controller_configuration.md)

### Externalize Secrets

You can also have an external provider create a secret for you in the namespace before deploying the chart with Helm. If the secret is pre-provisioned, replace the `agentToken` and `graphqlToken` arguments with:

```bash
--set agentStackSecret=<secret-name>
```

The format of the required secret can be found in [this file](./charts/agent-stack-k8s/templates/secrets.yaml.tpl).







### Other Installation Methods

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
