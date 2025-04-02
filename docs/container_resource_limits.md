# Default Resources

In the helm values, you can specify default resources to be used by the containers in Pods that are launched to run Jobs.

```yaml
# values.yaml
agentStackSecret: <name of predefend secrets for k8s>
config:
  org: <your-org-slug>
  pod-spec-patch:
    initContainers:
    - name: copy-agent
    requests:
      cpu: 100m
      memory: 50Mi
    limits:
      memory: 100Mi
    containers:
    - name: agent          # this container acquires the job
      resources:
        requests:
          cpu: 100m
          memory: 50Mi
        limits:
          memory: 1Gi
    - name: checkout       # this container clones the repo
      resources:
        requests:
          cpu: 100m
          memory: 50Mi
        limits:
          memory: 1Gi
    - name: container-0    # the job runs in a container with this name by default
      resources:
        requests:
          cpu: 100m
          memory: 50Mi
        limits:
          memory: 1Gi
```

and then every job that's handled by this installation of agent-stack-k8s will default to these values. To override it for a step, use a step level `podSpecPatch`.

```yaml
# pipelines.yaml
agents:
  queue: kubernetes
steps:
- name: Hello from a container with more resources
  command: echo Hello World!
  plugins:
  - kubernetes:
      podSpecPatch:
        containers:
        - name: container-0    # <---- You must specify this as exactly `container-0` for now.
          resources:           #       We are experimenting with ways to make it more ergonomic
            requests:
              cpu: 1000m
              memory: 50Mi
            limits:
              memory: 1Gi

- name: Hello from a container with default resources
  command: echo Hello World!
```
