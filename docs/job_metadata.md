# Default Job Metadata

The `agent-stack-k8s` controller can automatically add labels and annotations to the Kubernetes Jobs it creates.

## Controller Configuration

Default annotations and labels can be set in the controller's configuration values YAML file, via `default-metadata`.
This will apply the defined annotations and labels to all Jobs created by the controller:

```yaml
# values.yaml
...
default-metadata:
  annotations:
    imageregistry: "https://hub.docker.com/"
    mycoolannotation: llamas
  labels:
    argocd.argoproj.io/tracking-id: example-id-here
    mycoollabel: alpacas
...
```

## `kubernetes` Plugin

Default labels can be set for individual steps in a pipeline using the `metadata` config of the `kubernetes` plugin:

```yaml
# pipeline.yaml
...
  plugins:
    - kubernetes:
        metadata:
          annotations:
            imageregistry: "https://hub.docker.com/"
            mycoolannotation: llamas
          labels:
            argocd.argoproj.io/tracking-id: example-id-here
            mycoollabel: alpacas
...
```
