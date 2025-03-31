# Setting agent configuration (v0.16.0 and later)

> [!NOTE]
> Requires `v0.16.0` or newer

## Agent Configuration Options
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


## Pipeline signing

> [!NOTE]
> Requires `v0.16.0` or newer

#### `config/agent-config/signing-jwks-file`
(Optional)
Specifies the relative/absolute path of the JWKS file containing a signing key.
When an absolute path is provided, the will be the mount path for the JWKS file.
When a relative path (or filename) is provided, this will be appended to `/buildkite/signing-jwks` to create the mount path for the JWKS file.
(default: `key`)
```
config:
  agent-config:
    signing-jwks-key-file: key
```

#### `config/agent-config/signing-jwks-key-id`
(Optional)
The value provided via `--key-id` during JWKS key pair generation.
If not provided and the JWKS file contains only one key, that key will be used.
```
config:
  agent-config:
    signing-jwks-key-id: my-key-id
```

#### `config/agent-config/signingJWKSVolume`
(Optional)
Creates a Kubernetes Volume, which is mounted to the user-defined command containers at the path specified by `config/agent-config/signing-jwks-file`, containing JWKS signing key data from a Kubernetes Secret.
```
config:
  agent-config:
    signingJWKSVolume:
      name: buildkite-signing-jwks
      secret:
        secretName: my-signing-key
```

#### `config/agent-config/verification-jwks-file`
(Optional)
Specifies the relative/absolute path of the JWKS file containing a verification key.
When an absolute path is provided, the will be the mount path for the JWKS file.
When a relative path (or filename) is provided, this will be appended to `/buildkite/verification-jwks` to create the mount path for the JWKS file.
(default: `key`)
```
config:
  agent-config:
    verification-jwks-key-file: key
```

#### `config/agent-config/verification-failure-behavior`
(Optional)
This setting determines the Buildkite agent's response when it receives a job without a proper signature, and also specifies how strictly the agent should enforce signature verification for incoming jobs.
Valid options are `warn` and `block`.
When set to `warn`, the agent will emit a warning about missing or invalid signatures, but will still proceed to execute the job.
If not explicitly specified, the default behavior is `block`, which prevents any job without a valid signature from running, ensuring a secure pipeline environment by default.
(default: `block`)
```
config:
  agent-config:
    verification-failure-behavior: warn
```

#### `config/agent-config/verificationJWKSVolume`
(Optional)
Creates a Kubernetes Volume, which is mounted to the `agent` containers at the path specified by `config/agent-config/verification-jwks-file`, containing JWKS verification key data from a Kubernetes Secret.
```
config:
  agent-config:
    verificationJWKSVolume:
      name: buildkite-verification-jwks
      secret:
        secretName: my-verification-key
```
