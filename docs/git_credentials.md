# Configure Git Credentials
This document outlines how to configure your GitParameters correctly.

## Cloning (Private) Repos

Just like with a standalone installation of the Buildkite Agent, in order to access and clone private repos you will need to make Git credentials available for the Agent to use.
These credentials can be in the form of a SSH key for cloning over `ssh://` or with a `.git-credentials` file for cloning over `https://`.

### Cloning Repos Using SSH Keys

To use SSH to clone your repos, you'll need to create a Kubernetes Secret containing an authorized SSH private key and configure `agent-stack-k8s` to mount this Secret into the `checkout` container that is used to perform the Git repository cloning.

#### Create Kubernetes Secret From SSH Private Key

> [!WARNING]  
> Support for DSA keys has been removed from OpenSSH as of early 2025. This removal now affects `buildkite/agent` version `v3.88.0` and newer. Please migrate to `RSA`, `ECDSA`, or `EDDSA` keys.

After creating a SSH keypair and registering its public key with the remote repository provider (e.g. [GitHub](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/adding-a-new-ssh-key-to-your-github-account)), you can create a Kubernetes Secret using the SSH private key file.

> [!NOTE]
> Ensure the environment variable name matches the recognized names (`SSH_PRIVATE_*_KEY`) in [`docker-ssh-env-config`](https://github.com/buildkite/docker-ssh-env-config).
> * `SSH_PRIVATE_ECDSA_KEY`
> * `SSH_PRIVATE_ED25519_KEY`
> * `SSH_PRIVATE_RSA_KEY`
> A script from this project is included in the default entrypoint of the default [`buildkite/agent`](https://hub.docker.com/r/buildkite/agent) Docker image.
> It will process the value of the secret and write out a private key to the `~/.ssh` directory of the checkout container.

To create a Kubernetes Secret named `my-git-ssh-credentials` containing the contents of the SSH private key file `$HOME/.ssh/id_rsa`:

```bash
kubectl create secret generic my-git-ssh-credentials --from-file=SSH_PRIVATE_RSA_KEY="$HOME/.ssh/id_rsa"
```

This Secret can be referenced by the `agent-stack-k8s` controller using [EnvFrom](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/#configure-all-key-value-pairs-in-a-secret-as-container-environment-variables) from within the controller configuration or via the `gitEnvFrom` config of the `kubernetes` plugin.

#### Provide Kubernetes Secret via Configuration

Using [`pod-spec-patch`](need-link-to-configuration-value-page), you can specify the Kubernetes Secret containing your SSH private key in your configuration values YAML file using `envFrom`:

```yaml
# values.yaml
...
config:
  ...
  pod-spec-patch:
    containers:
    - name: checkout                    # <---- this is needed so that the secret will only be mounted on the checkout container
      envFrom:
      - secretRef:
          name: my-git-ssh-credentials  # <---- this is the name of the Kubernetes Secret (ex. my-git-ssh-credentials, from command above)
```

#### Provide Kubernetes Secret via `kubernetes` Plugin

Under the `kubernetes` plugin, specify the name of the Kubernetes Secret via the `gitEnvFrom` config:

```yaml
# pipeline.yaml
steps:
- label: :kubernetes: Hello World!
  command: echo Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      gitEnvFrom:
      - secretRef:
          name: my-git-ssh-credentials  # <---- this is the name of the Kubernetes Secret (ex. my-git-ssh-credentials, from command above)
```

> [!NOTE]
> Using the `kubernetes` plugin to provide the SSH private key Secret will need to be **defined under every step** accessing a private Git repository.
> Defining this Secret using the controller configuration means that it only needs to be configured once.


#### Provide SSH Private Key to Non-`checkout` Containers

The above configurations provide the SSH private key as a Kubernetes Secret to only the `checkout` container. If Git SSH credentials are required in user-defined job
containers, there are a few options:
* Use a container image based on the default `buildkite/agent` Docker image, which preserves the default entrypoint by not overriding the command in the job spec
* Include or reproduce the functionality of the [`ssh-env-config.sh`](https://github.com/buildkite/docker-ssh-env-config/blob/-/ssh-env-config.sh) script in the entrypoint for your job container image to source from recognized environment variable names

### Cloning Repos Using Git Credentials

To use HTTPS to clone private repos, you can use a `.git-credentials` file stored in a secret, and
refer to this secret using the `gitCredentialsSecret` checkout parameter.

#### Create Kubernetes Secret From Git Credentials File

Create a `.git-credentials` file formatted in the manner expected by the `store` [Git credential helper](https://git-scm.com/docs/git-credential-store).
After this file has been created, you can create a Kubernetes Secret containing the contents of this file:

```bash
kubectl create secret generic my-git-https-credentials --from-file='.git-credentials'="$HOME/.git-credentials"
```

#### Provide Kubernetes Secret via Configuration

Using `default-checkout-params`, you can define your Kubernetes Secret as follows:

```yaml
# values.yaml
...
config:
  ...
  default-checkout-params:
    gitCredentialsSecret:
      secretName: my-git-https-credentials
```

If you wish to use a different key within the Kubernetes Secret than `.git-credentials`, you can
[project it](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/#project-secret-keys-to-specific-file-paths)
to `.git-credentials` by using `items` within `gitCredentialsSecret`.

```yaml
# values.yaml
...
default-checkout-params:
  gitCredentialsSecret:
    secretName: my-git-https-credentials
    items:
    - key: funky-creds
      path: .git-credentials
```

#### Provide Kubernetes Secret via `kubernetes` Plugin

Under the `kubernetes` plugin, specify the name of the Kubernetes Secret via the `checkout.gitCredentialsSecret` config:

```yaml
# pipeline.yaml
steps:
- label: :kubernetes: Hello World!
  command: echo Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        gitCredentialsSecret:
          secretName: my-git-https-credentials  # <---- this is the name of the Kubernetes Secret (ex. my-git-https-credentials, from command above)
```

#### Provide Git Credentials to Non-`checkout` Containers

The above configurations provide Git credentials as a Kubernetes Secret to only the `checkout` container. If the `.git-credentials` file is required in user-defined job
containers, you can add a volume mount for the `git-credentials` volume, and configure Git to use the file within it (e.g. with `git config credential.helper 'store --file ...'`).

## Skipping checkout (v0.13.0 and later)

For some steps, you may wish to avoid checkout (cloning a source repository).
This can be done with the `checkout` block under the `kubernetes` plugin:

```yaml
steps:
- label: Hello World!
  agents:
    queue: kubernetes
  plugins:
  - kubernetes:
      checkout:
        skip: true # prevents scheduling the checkout container
```

## Using `default-checkout-params`

### `envFrom`

`envFrom` can be added to all checkout, command, and sidecar containers
separately, either per-step in the pipeline or for all jobs in `values.yaml`.

Pipeline example (note that the blocks are `checkout`, `commandParams`, and
`sidecarParams`):

```yaml
# pipeline.yml
...
  kubernetes:
    checkout:
      envFrom:
      - prefix: GITHUB_
        secretRef:
          name: github-secrets
    commandParams:
      interposer: vector
      envFrom:
      - prefix: DEPLOY_
        secretRef:
          name: deploy-secrets
    sidecarParams:
      envFrom:
      - prefix: LOGGING_
        configMapRef:
          name: logging-config
```

`values.yaml` example:

```yaml
# values.yml
config:
  default-checkout-params:
    envFrom:
    - prefix: GITHUB_
      secretRef:
        name: github-secrets
  default-command-params:
    interposer: vector
    envFrom:
    - prefix: DEPLOY_
      secretRef:
        name: deploy-secrets
  default-sidecar-params:
    envFrom:
    - prefix: LOGGING_
      configMapRef:
        name: logging-config
```
