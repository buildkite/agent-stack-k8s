# Buildkite agent stack for k8s

This is an early release, but should help you evaluate Buildkite for your POC.

## Environment Setup

You'll need to have the docker community edition installed to run the local tests with kind (kubernetes-in-docker). We recommend installing it directly from [https://www.docker.com/get-started/](https://www.docker.com/get-started/)

The rest of the local development environment dependencies are managed with homebrew and can be installed with the bootstrap script.

```
./bin/bootstrap
```

## Configuring the stack

You'll find several parts inside the buildkite.yaml manifest that need real values. You will need to configure:
1. Buildkite agent token
2. Buildkite agent git credentials
3. Buildkite agent ssh key

### Agent token

The buildkite agent token can be found here for your GitHub organization.
https://buildkite.com/organizations/MY_ORGANIZATION_SLUG/agents

To encode and copy the buildkite agent token run the below and paste that value into `buildkite.yaml` where it reads "PASTE_AGENT_SECRET_HERE"

```
printf "<PASTE_BUILDKITE_AGENT_TOKEN>" | base64 | pbcopy
```

### Agent git credentials

You can create a set of git credentials for testing on GitHub [here](https://github.com/settings/tokens). You only need to select repository access.

To encode and copy the git credentials run the below and paste that value into `buildkite.yaml` where it reads "PASTE_GIT_CREDENTIALS_HERE"

```
printf "https://<MY_GITHUB_USERNAME>:<MY_ACCESS_TOKEN>@github.com" | base64 | pbcopy
```

### Agent ssh key

You can either [make the agent its own ssh key](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/generating-a-new-ssh-key-and-adding-it-to-the-ssh-agent) and [add it as a deploy key to the repositories you want to test](https://docs.github.com/en/developers/overview/managing-deploy-keys) or for simplicity with local testing use your own ssh key.

To encode and copy the git credentials run the below and paste that value into `buildkite.yaml` where it reads "PASTE_SSH_KEY_HERE"

```
cat ~/.ssh/id_rsa | base64 | pbcopy
```

## Running the stack locally

The easiest way to get started is to run a kind(kubernetes-in-docker) cluster on your local machine. We have a few scripts that make cluster provisioning and bootstrap easier.

To get started run the command below. This will setup a single node kubernetes cluster in docker, add the kubernetes metric server, and then add manifests for the buildkite kubernetes stack.

```
./bin/up
```

When you are finished tear it down with the command below. This deletes all the resources out of the cluster and

```
./bin/down
```

## Running steps in a pod

The example uses the [buildkite k8s job plugin](https://github.com/buildkite-plugins/k8s-job-buildkite-plugin) to allow running Buildkite Jobs in a Kubenetes Job using a Kubernetes Pod Spec. An couple example pipelines are included in this example as well as the source Dockerfiles for those make believe containers. If you need to network between containers in a pod step you can use [kubedns](https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/) to talk between containers.

There are a couple really simple test services -- [win penguin](https://github.com/buildkite/k8s-agent-stack/) and [fail whale](https://github.com/buildkite/k8s-agent-stack). We also pushed the images for [win penguin](https://hub.docker.com/repository/docker/deftinc/winpenguin/) and [fail whale](https://hub.docker.com/repository/docker/deftinc/failwhale) up to Dockerhub for your testing.

## Agent Scaling

The example scales [buildkite agent](https://github.com/buildkite/agent) pods by [horizontal pod autoscaling](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/) and [buildkite metrics](https://github.com/elotl/buildscaler) from the default job queue. Whenever there are scheduled jobs waiting for execution the number of agent boxes scale up by either double or add 7 agents whichever is greater every 30 seconds. Whenever there are idle agent boxes they will begin to scale down 1 box every 20 seconds, but there may appear to be a delay if that box is currently running a job.

## Known limitations
1. Build steps running in a pod spec are currently limited to checking the status code of the first container in the container list.
2. Agent scaling is pre-defined at the moment to show the functionality, but will be customizeable in the future.
