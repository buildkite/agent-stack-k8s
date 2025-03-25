# Buildkite Agent Stack for Kubernetes

[![Build status](https://badge.buildkite.com/d58c90abfe8b48f8d8750dac8e911fc0b6afe026631b4dc97c.svg?branch=main)](https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack)


A Kubernetes controller that runs [Buildkite steps](https://buildkite.com/docs/pipelines/defining-steps) as [Kubernetes jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/).

## Requirements

- A Kubernetes cluster
- An API token with the [GraphQL scope enabled](https://buildkite.com/docs/apis/graphql-api#authentication)
- An [agent token](https://buildkite.com/docs/agent/v3/tokens)
- A Buildkite [cluster's UUID](#buildkite-clusters-uuid)

## Deploy with Helm

You'll need Helm version 3.8.0 or newer since we're using Helm's support for [OCI-based registries](https://helm.sh/docs/topics/registries/).

The simplest way to get up and running is by deploying our [Helm](https://helm.sh) chart:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --create-namespace \
    --namespace buildkite \
    --set config.org=<your Buildkite org slug> \
    --set agentToken=<your Buildkite agent token> \
    --set graphqlToken=<your Buildkite GraphQL-enabled API token> \
    --set config.cluster-uuid=<your Buildkite cluster's UUID>
```
This will create an agent-stack-k8s installation that will listen to the `kubernetes` queue.

See the `--tags` [option](#Options) for specifying a different queue. 

See [here](#buildkite-clusters-uuid) for more info on the cluster's UUID.

## Documentation

For further options, overview, and docs, see [Docs](/docs/getting_started.md).
