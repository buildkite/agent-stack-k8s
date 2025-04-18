# Volume mounts

In some situations, for example if you want to use [git mirrors](https://buildkite.com/docs/agent/v3#promoted-experiments-git-mirrors), you may want to attach extra volume mounts (in addition to the `/workspace` one) to some or all of the pod containers.

## All containers

In order to attach extra volume mounts to all containers (`checkout`, `agent`, `command`, `sidecar`, etc.) you can use the `kubernetes` plugin:

```
steps:
  - label: ":file_cabinet: Share file across containers using volume mount"
    key: share-file-using-scratch-volume
    env:
      SCRATCH_VOLUME_PATH: "/tmp/scratch"
      SCRATCH_VOLUME_PATH_TIMEOUT_SECONDS: "10"
    plugins:
      - kubernetes:
          podSpec:
            containers:
              - image: alpine:latest
                command:
                  - touch $${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt
              - image: alpine:latest
                command:
                  - |-
                    COUNT=0
                    until [[ $$((COUNT++)) == $${SCRATCH_VOLUME_PATH_TIMEOUT_SECONDS} ]]; do
                      [[ -f "$${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt" ]] && break
                      echo "⚠️  Waiting for $${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt to be written... (Attempt $${COUNT}/$${SCRATCH_VOLUME_PATH_TIMEOUT_SECONDS})"
                      sleep 1
                    done

                    if ! [[ -f "$${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt" ]]; then
                      echo "⛔ $${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt has not been written"
                      exit 1
                    fi

                    echo "✅ $${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt has been written"
                    rm -f "$${SCRATCH_VOLUME_PATH}/foo-$${BUILDKITE_JOB_ID}.txt"
            volumes:
              - name: scratch-volume
                hostPath:
                  path: "/tmp/volumes/scratch"
                  type: DirectoryOrCreate
          extraVolumeMounts:
            - name: scratch-volume
              mountPath: /tmp/scratch
```

## `checkout` containers

In order to attach extra volumes to your `checkout` containers, define `config.default-checkout-params.extraVolumeMounts` in your configuration. Example:

```yaml
# values.yaml
config:
  default-checkout-params:
    gitCredentialsSecret:
      secretName: my-git-credentials
    extraVolumeMounts:
      - name: checkout-extra-dir
        mountPath: /extra-checkout
  pod-spec-patch:
    containers:
      - name: checkout
        image: "buildkite/agent:latest"
    volumes:
      - name: checkout-extra-dir
        hostPath:
          path: /my/extra/dir/checkout
          type: DirectoryOrCreate
```

Or `checkout.extraVolumeMounts` in the `kubernetes` plugin. Example:

```yaml
# pipeline.yml
...
  kubernetes:
    checkout:
      extraVolumeMounts:
        - name: checkout-extra-dir
          mountPath: /extra-checkout
    podSpecPatch:
      containers:
        - name: checkout
          image: "buildkite/agent:latest"
      volumes:
        - name: checkout-extra-dir
          hostPath:
            path: /my/extra/dir/checkout
            type: DirectoryOrCreate
```

## `command` containers

In order to attach extra volumes to your `container-#` (`command`) containers, define `config.default-command-params.extraVolumeMounts` in your configuration. Example:

```yaml
# values.yaml
config:
  default-command-params:
    extraVolumeMounts:
      - name: command-extra-dir
        mountPath: /extra-command
  pod-spec-patch:
    containers:
      - name: container-0
        image: "buildkite/agent:latest"
    volumes:
      - name: command-extra-dir
        hostPath:
          path: /my/extra/dir/command
          type: DirectoryOrCreate
```

Or `commandParams.extraVolumeMounts` in the `kubernetes` plugin. Example:

```yaml
# pipeline.yml
...
  kubernetes:
    commandParams:
      extraVolumeMounts:
        - name: command-extra-dir
          mountPath: /extra-command
    podSpecPatch:
      containers:
        - name: container-0
          image: "buildkite/agent:latest"
      volumes:
        - name: command-extra-dir
          hostPath:
            path: /my/extra/dir/command
            type: DirectoryOrCreate
```

## `sidecar` containers

In order to attach extra volumes to your `sidecar` containers, define `config.default-sidecar-params.extraVolumeMounts` in your configuration. Example:

```yaml
# values.yaml
config:
  default-sidecar-params:
    extraVolumeMounts:
      - name: sidecar-extra-dir
        mountPath: /extra-sidecar
  pod-spec-patch:
    containers:
      - name: checkout
        image: "buildkite/agent:latest"
    volumes:
      - name: sidecar-extra-dir
        hostPath:
          path: /my/extra/dir/sidecar
          type: DirectoryOrCreate
```

Or `sidecarParams.extraVolumeMounts` in the `kubernetes` plugin. Example:

```yaml
# pipeline.yml
...
  kubernetes:
    sidecars:
      - image: nginx:latest
    sidecarParams:
      extraVolumeMounts:
        - name: sidecar-extra-dir
          mountPath: /extra-sidecar
    podSpecPatch:
      containers:
        - name: checkout
          image: "buildkite/agent:latest"
      volumes:
        - name: sidecar-extra-dir
          hostPath:
            path: /my/extra/dir/sidecar
            type: DirectoryOrCreate
```
