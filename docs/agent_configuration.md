# Setting agent configuration (v0.16.0 and later)

The `agent-config` block within `values.yaml` can be used to set a subset of
[the agent configuration file options](https://buildkite.com/docs/agent/v3/configuration).

```yaml
# values.yaml
config:
  agent-config:
    no-http2: false
    experiment: ["use-zzglob", "polyglot-hooks"]
    shell: "/bin/bash"
    no-color: false
    strict-single-hooks: true
    no-multipart-artifact-upload: false
    trace-context-encoding: json
    disable-warnings-for: ["submodules-disabled"]
    no-pty: false
    no-command-eval: true
    no-local-hooks: true
    no-plugins: true
    plugin-validation: false
```

Note that even if `no-command-eval` or `no-plugins` is enabled, the Kubernetes
plugin may still be able to override everything, since it is interpreted by the
stack controller, not the agent. `no-command-eval` or `no-plugins` should be
used together with [`prohibit-kubernetes-plugin`](#securing-stack).