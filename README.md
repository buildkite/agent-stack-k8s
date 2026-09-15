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

### Use a unique stack ID for each installation

The controller registers itself with the Buildkite Agent API using its controller
ID as the stack key, and deregisters that key when it shuts down. Stack keys must
be unique within a Buildkite organization, not only within a Kubernetes cluster
or namespace. See the Agent API documentation for
[registering](https://buildkite.com/docs/apis/agent-api/stacks#register-a-stack)
and [deregistering](https://buildkite.com/docs/apis/agent-api/stacks#de-register-a-stack)
a stack.

The Helm chart sets the controller ID to the Helm release's full name. If
independent installations in different Kubernetes clusters or namespaces use the
same release name, they register the same stack key. Registration is idempotent,
so this may initially appear to work, but shutting down either controller
deregisters the key used by the others. The remaining controllers may then
receive `Stack not found with the provided key` errors.

Give every independently operated installation in the organization a unique,
stable Helm release name:

```sh
helm upgrade --install agent-stack-k8s-us-east \
    oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --set agentToken=<buildkite-agent-token>
```

Alternatively, set a unique `fullnameOverride`, or set
`BUILDKITE_K8S_STACK_CONTROLLER_ID` using `controllerEnv` if Kubernetes resource
names must remain unchanged. Non-Helm deployments can set the same environment
variable or pass `--id`; without an ID, the controller uses `agent-stack-k8s` as
the stack key.

Full instructions can be found [in the documentation](https://buildkite.com/docs/agent/v3/agent-stack-k8s/installation).

## Documentation

Comprehensive documentation for the Buildkite Agent Stack for Kubernetes controller can be found in the [Agent Stack for Kubernetes section of the Buildkite Docs](https://buildkite.com/docs/agent/v3/agent-stack-k8s).

## Development

For guidelines and requirements regarding contributing to the Buildkite Agent Stack for Kubernetes controller, please see the [Development guide](DEVELOPMENT.md).
