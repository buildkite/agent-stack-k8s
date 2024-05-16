Agent Stack K8s Development
===

# Local Dependencies
Install dependencies with Homebrew via:

```bash
brew bundle
```

Run tasks via [just](https://github.com/casey/just):

```bash
just --list
```

# Integration Tests

## Setup
For running the integration tests you'll need to add some additional scopes to your Buildkite API token:

- `read_artifacts`
- `read_build_logs`
- `write_pipelines`

You'll also need to create an SSH secret in your cluster to run [this test pipeline](internal/integration/fixtures/secretref.yaml). This SSH key needs to be associated with your GitHub account to be able to clone this public repo, and must be in a form acceptable to OpenSSH (aka `BEGIN OPENSSH PRIVATE KEY`, not `BEGIN PRIVATE KEY`).

```bash
kubectl create secret generic agent-stack-k8s --from-file=SSH_PRIVATE_RSA_KEY=$HOME/.ssh/id_github
```

## Debugging
The integration tests on the [`kubernetes-agent-stack`](https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack) pipeline will create additional pipelines in the [`buildkite-kubernetes-stack`](https://buildkite.com/buildkite-kubernetes-stack) organization.


## Cleanup
These will be deleted automatically for successful tests, but for unsuccessful tests, then will remain after then end of the test job to allow you to debug them.
However, this means they should be cleaned up manually. To do this run
```bash
CLEANUP_PIPELINES=true just cleanup-orphans --org=buildkite-kubernetes-stack --buildkite-token=<buildkite-api-token>
```

The token will need to have graphql access as well as:
- `read_artifacts`
- `write_pipelines`

This is usually enough, but there is another situation where the cluster could be clogged with K8s jobs.
To clean these out you should run the following in a kubernetes context in the namespace containing the controller used to run the CI pipeline.
```bash
kubectl get -o jsonpath='{.items[*].metadata.name}' jobs | xargs -L1 kubectl delete job
```

At the time of writing, the CI pipeline is run in an EKS cluster, `agent-stack-k8s-ci` in the `buildkite-agent` AWS account.
The controller is deployed to the `buildkite` namespace in that cluster.
See https://docs.aws.amazon.com/eks/latest/userguide/create-kubeconfig.html for how to obtain a kubeconfig for an EKS cluster.

# Run from source

First store the agent token in a Kubernetes secret:

```bash!
kubectl create secret generic buildkite-agent-token --from-literal=BUILDKITE_AGENT_TOKEN=my-agent-token
```

Next start the controller:

```bash!
just run --org my-org --buildkite-token my-api-token --debug
```

## Local Deployment with Helm

`just deploy` will build the container image using [ko](https://ko.build/) and
deploy it with [Helm](https://helm.sh/).

You'll need to have set `KO_DOCKER_REPO` to a repository you have push access
to. For development something like the [kind local
registry](https://kind.sigs.k8s.io/docs/user/local-registry/) or the [minikube
registry](https://minikube.sigs.k8s.io/docs/handbook/registry) can be used. More
information is available at [ko's
website](https://ko.build/configuration/#local-publishing-options).

You'll also need to provide required configuration values to Helm, which can be done by passing extra args to `just`:

```bash
just deploy --values config.yaml
```

With config.yaml being a file containing [required Helm values](values.yaml), such as:

```yaml
agentToken: "abcdef"
graphqlToken: "12345"
config:
  org: "my-buildkite-org"
```

The `config` key contains configuration passed directly to the binary, and so supports all the keys documented in [the example](examples/config.yaml).

# Release
1. Make sure you're on the main branch!
1. Create a tag
```bash
git tag -sm v0.x.x v0.x.x
```
1. Push your tag
```bash
git push --tags
```
1. A build will start at https://buildkite.com/buildkite-kubernetes-stack/kubernetes-agent-stack/builds?branch=v0.x.x. It will create a draft release with a changelog. Edit the changelog to group the PRs in to sections like
```
# Added
# Fixed
# Changed
# Security
# Internal
```
1. Publish the release 🎉
