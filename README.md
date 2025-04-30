# Buildkite Agent Stack for Kubernetes

[![Build status](https://badge.buildkite.com/d58c90abfe8b48f8d8750dac8e911fc0b6afe026631b4dc97c.svg?branch=main)](https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack)

## Overview

`agent-stack-k8s` is a Kubernetes [controller](https://kubernetes.io/docs/concepts/architecture/controller/) that uses the Buildkite [Agent API](https://buildkite.com/docs/apis/agent-api) to watch for scheduled jobs assigned to the controller's queue.

> [!NOTE] Starting with v0.28.0, the Buildkite GraphQL API is no longer used. If you are upgrading from an older version, your GraphQL-enabled token can be safely removed from your configuration or Kubernetes secret. Only the Agent token is required.

## Requirements

- A Kubernetes cluster
- A Cluster [Agent Token](https://buildkite.com/docs/agent/v3/tokens#create-a-token)
- A Cluster [Queue](https://buildkite.com/docs/pipelines/clusters/manage-queues#create-a-self-hosted-queue)
  - The UUID of this Cluster is also required. See [Obtain Cluster UUID](docs/installation.md#how-to-find-a-buildkite-clusters-uuid)

## Installation

For installation, see [Installation](docs/installation.md)

## Documentation

Documentation can be found under [`docs/`](docs/README.md).

For a broader context of what is Buildkite, see the [Buildkite Documentation](https://buildkite.com/docs).
