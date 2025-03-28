# Pipeline Signing

> [!NOTE]
> Requires `v0.16.0` or newer

The `agent-stack-k8s` controller supports Buildkite's [Signed Pipelines](https://buildkite.com/docs/agent/v3/signed-pipelines) feature. A JWKS key pair is stored as Kubernetes Secrets and mounted to the `agent` and user-defined command containers.

## Generate JWKS Key Pair

Using the `buildkite-agent` CLI, [generate a JWKS key pair](https://buildkite.com/docs/agent/v3/signed-pipelines#self-managed-key-creation-step-1-generate-a-key-pair):

```shell
buildkite-agent tool keygen --alg EdDSA --key-id my-jwks-key
```

This will create a pair of files in the current directory:

```
EdDSA-my-jwks-key-private.json
EdDSA-my-jwks-key-public.json
```

## Create Kubernetes Secrets for JWKS Key Pair

After using `buildkite-agent` to generate a JWKS key pair, create a Kubernetes Secret for the JWKS signing key that will be used by user-defined command containers:

```shell
kubectl create secret generic my-signing-key --from-file='key'="./EdDSA-my-jwks-key-private.json"
```

Now create a Kubernetes Secret for the JWKS verification key that will be used by the `agent` container:

```shell
kubectl create secret generic my-verification-key --from-file='key'="./EdDSA-my-jwks-key-public.json"
```

## Update Configuration Values File

To use the Kubernetes Secrets containing your JWKS key pair, update the `agent-config` of your configuration values YAML file:

```yaml
# values.yaml
config:
  agent-config:
    signing-jwks-file: key
    signing-jwks-key-id: my-jwks-key
    signingJWKSVolume:
      name: buildkite-signing-jwks
      secret:
        secretName: my-signing-key

    verification-jwks-file: key
    verification-failure-behavior: warn # optional, default behavior is 'block'
    verificationJWKSVolume:
      name: buildkite-verification-jwks
      secret:
        secretName: my-verification-key
```

Additional information on configuring JWKS key pairs for signing/verification can be found in [agent configuration docs](agent_configuration.md#pipeline-signing).
