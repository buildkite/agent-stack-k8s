# Long-running jobs

With the addition of `.spec.job.activeDeadlineSeconds` in version [`v0.24.0`](https://github.com/buildkite/agent-stack-k8s/releases/tag/v0.24.0), Kubernetes jobs will run for a (default) maximum duration of `21600` seconds (6 hours). After this duration has been exceeded, all of the running Pods are terminated and the Job status will be `type: Failed`. This will be reflected in the Buildkite UI as `Exited with status -1 (agent lost)`.

If long-running jobs are common in your Buildkite Organization, this value should be increased in your controller configuration:
```yaml
# values.yaml
...
config:
  job-active-deadline-seconds: 86400 # 24h
...
```

It is also possible to override this configuration via the `kubernetes` plugin directly in your pipeline steps and will only apply to that `command` step:
```yaml
steps:
- label: Long-running job
  command: echo "Hello world" && sleep 43200
  plugins:
  - kubernetes:
      jobActiveDeadlineSeconds: 43500
```

