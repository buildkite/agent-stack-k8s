# How to set up pipeline signing (v0.16.0 and later)

The `agent-config` block within `values.yaml` accepts most of the
[Signed Pipelines](https://buildkite.com/docs/agent/v3/signed-pipelines) options.

Additionally, volume sources for signing and verification keys can be specified and automatically attached to the right containers.

For keys, any volume source can be specified but a common choice is to use a
`secret` source. Keys are generally small, distributed across the cluster,
and ideally are never shown in plain text.

1.  Create one or two secrets containing signing and verification keys:
    ```shell
    kubectl create secret generic my-signing-key --from-file='key'="$HOME/private.jwk"
    kubectl create secret generic my-verification-key --from-file='key'="$HOME/public.jwk"
    ```

2.  Add `values.yaml` configuration to use the volumes:
    ```yaml
    # values.yaml
    config:
      agent-config:
        # The signing key will be attached to command containers, so it can be
        # used by 'buildkite-agent pipeline upload'.
        signing-jwks-file: key # optional if the file within the volume is called "key"
        signing-jwks-key-id: llamas # optional
        signingJWKSVolume:
          name: buildkite-signing-jwks
          secret:
            secretName: my-signing-key
        # The verification key will be attached to the 'agent start' container.
        verification-jwks-file: key # optional if the file within the volume is called "key"
        verification-failure-behavior: warn # for testing/incremental rollout, use 'block' to enforce
        verificationJWKSVolume:
          name: buildkite-verification-jwks
          secret:
            secretName: my-verification-key
    ```

Note that `signing-jwks-file` and `verification-jwks-file` agent config options can be used to either change the mount point of the corresponding volume (with an absolute path) or specify a file within the volume (with a relative path).

The default mount points are `/buildkite/signing-jwks` and
`/buildkite/verification-jwks`.