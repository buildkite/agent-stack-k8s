# Securing Buildkite jobs on `agent-stack-k8s`

> [!NOTE]
> Requires `v0.13.0` or newer

The `prohibit-kubernetes-plugin` configuration option can be used to prevent users from overriding a controller-defined `pod-spec-patch`.
With the `prohibit-kubernetes-plugin` configuration enabled, any Buildkite job including the `kubernetes` plugin will fail.

## Inline configuration

Add the `--prohibit-kubernetes-plugin` argument to your Helm deployment:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --namespace buildkite \
    --create-namespace \
    --set agentToken=<Buildkite Cluster Agent Token> \
    --set graphqlToken=<Buildkite GraphQL-enabled API Access Token> \
    --set config.org=<Buildkite Org Slug> \
    --set config.cluster-uuid=<Buildkite Cluster UUID> \
    --tags queue=kubernetes \
    --prohibit-kubernetes-plugin
```

## Configuration values YAML File

You can also enable the `prohibit-kubernetes-plugin` option in your configuration values YAML file:

```yaml
# values.yaml
...
config:
  prohibit-kubernetes-plugin: true
  pod-spec-patch:
    # Override the default podSpec here.
  ...
```
