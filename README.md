# Buildkite Agent Stack for Kubernetes

[![Build status](https://badge.buildkite.com/d58c90abfe8b48f8d8750dac8e911fc0b6afe026631b4dc97c.svg?branch=main)](https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack)

## Overview

The Buildkite Agent Stack for Kubernetes (also known as `agent-stack-k8s`) is a Kubernetes [controller](https://kubernetes.io/docs/concepts/architecture/controller/) that uses the Buildkite [Agent API](https://buildkite.com/docs/apis/agent-api) to watch for scheduled jobs assigned to the controller's queue.

> [!NOTE]
> Starting with v0.28.0, the Buildkite GraphQL API is no longer used. If you are upgrading from an older version, your GraphQL-enabled token can be safely removed from your configuration or Kubernetes secret. Only the Agent token is required.

## Requirements

- A Kubernetes cluster.
- A [Buildkite cluster](https://buildkite.com/docs/pipelines/clusters/manage-clusters) and [agent token](https://buildkite.com/docs/agent/v3/tokens) for this cluster.
- A [self-hosted queue](https://buildkite.com/docs/pipelines/clusters/manage-queues#create-a-self-hosted-queue) for this Buildkite cluster.
  - The UUID of this Buildkite cluster is also required. Learn how to do this in [How to find a Buildkite cluster's UUID](https://buildkite.com/docs/agent/v3/agent-stack-k8s/installation#how-to-find-a-buildkite-clusters-uuid).

## Documentation

Comprehensive documentation for the Buildkite Agent Stack for Kubernetes controller can be found in the [Agent Stack for Kubernetes section of the Buildkite Docs](https://buildkite.com/docs/agent/v3/agent-stack-k8s).

### Installation

Installation instructions can be found on the [Installation](https://buildkite.com/docs/agent/v3/agent-stack-k8s/installation) page of the Agent Stack for Kubernetes documentation.

## Development

For guidelines and requirements regarding contributing to the Buildkite Agent Stack for Kubernetes controller, please see the [Development guide](DEVELOPMENT.md).
