# Buildkite Agent Stack for Kubernetes

[![Build status](https://badge.buildkite.com/d58c90abfe8b48f8d8750dac8e911fc0b6afe026631b4dc97c.svg?branch=main)](https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack)

## Overview

The Buildkite Agent Stack for Kubernetes (also known as `agent-stack-k8s`) is a Kubernetes [controller](https://kubernetes.io/docs/concepts/architecture/controller/) that uses the Buildkite [Agent API](https://buildkite.com/docs/apis/agent-api) to watch for scheduled jobs assigned to the controller's queue.

> [!NOTE]
> Starting with v0.28.0, the Buildkite GraphQL API is no longer used. If you are upgrading from an older version, your GraphQL-enabled token can be safely removed from your configuration or Kubernetes secret. Only the Agent token is required.

## Requirements

- A Kubernetes cluster.
- A [Buildkite Agent Token](https://buildkite.com/organizations/~/clusters/~/tokens).

## Usage

The simplest way to launch a stack on the default queue:

```sh
helm install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --set agentToken=<buildkite-agent-token>
```

To specify a non-default queue:

```sh
helm install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --set agentToken=<buildkite-agent-token> \
    --set config.queue=arm64
```

### Multiple independent controllers serving the same queue

Most installations need no controller ID configuration. If multiple independent
controller installations service the same queue, give each installation a
distinct controller ID (its Agent API stack key), stable across restarts and
upgrades.

With Helm, use distinct release full names or set
`BUILDKITE_K8S_STACK_CONTROLLER_ID` through `controllerEnv` to leave Kubernetes
resource names unchanged. Non-Helm deployments can set the same environment
variable or pass `--id`.

Full instructions can be found [in the documentation](https://buildkite.com/docs/agent/v3/agent-stack-k8s/installation).

## Documentation

Comprehensive documentation for the Buildkite Agent Stack for Kubernetes controller can be found in the [Agent Stack for Kubernetes section of the Buildkite Docs](https://buildkite.com/docs/agent/v3/agent-stack-k8s).

## Development

For guidelines and requirements regarding contributing to the Buildkite Agent Stack for Kubernetes controller, please see the [Development guide](DEVELOPMENT.md).
