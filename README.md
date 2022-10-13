# Buildkite Agent Stack for Kubernetes

This is an early prototype for running an autoscaling [Buildkite Agent](https://github.com/buildkite/agent) stack on [Kubernetes](https://kubernetes.io).

We've seen many customers running the Agent on their own Kubernetes clusters. This is an extraction of some of the patterns we've seen. The stack works today, but we'll be improving it over time as we discover the best ways to run Buildkite pipelines on Kubernetes.

## Configuring the stack

You'll need to create your own overlay to add:
1. Buildkite agent token
2. Private repository access using either
   1. Git credentials
   2. SSH key

```
mkdir k8s/overlays/my-stackname
touch k8s/overlays/my-stackname/git-credentials
touch k8s/overlays/my-stackname/private-ssh-key
touch k8s/overlays/my-stackname/kustomization.yaml
```

Then create a file `k8s/overlays/my-stackname/kustomization.yaml` with contents similar to:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: buildkite

commonLabels:
  service: buildkite

bases:
  - ../../buildkite

secretGenerator:
  - name: buildkite-agent-token
    literals:
      - token=PASTE_AGENT_TOKEN_HERE
  - name: buildkite-secrets
    files:
      - ./git-credentials
      - ./private-ssh-key
```

Then you should either make your repository clone private OR add your overlay to your `.gitignore` file. These secrets should not be checked into a public repository.

```
k8s/overlays/my-stackname
```

### Agent token

The buildkite agent token can be found here for your GitHub organization.
https://buildkite.com/organizations/~/agents

Paste that value into the space labeled "PASTE_AGENT_TOKEN_HERE" in `/k8s/overlays/my-stackname`

### Private repository access

#### Git credentials

You can create a set of git credentials for testing on GitHub [here](https://github.com/settings/tokens). You only need to select repository access.

Create the git credentials file by running the command below with the template values filled in:

```
echo "https://<MY_GITHUB_USERNAME>:<MY_ACCESS_TOKEN>@github.com" > ./k8s/overlays/my-stackname/git-credentials
```


#### SSH key

We would recommend [making the agent its own ssh key](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/generating-a-new-ssh-key-and-adding-it-to-the-ssh-agent) and [adding it as a deploy key to the repository you want to test](https://docs.github.com/en/developers/overview/managing-deploy-keys), or using [a machine user with a dedicated ssh key](https://docs.github.com/en/developers/overview/managing-deploy-keys#machine-users). But for simplicity during local testing you can also use your own ssh key.

```
cp ~/.ssh/id_rsa ./k8s/overlays/my-stackname/private-ssh-key
```

## Viewing the generated manifests

You can view the generated manifests before apply them to the cluster with:

```
kustomize build k8s/overlays/my-stackname
```

You can pipe this input directly into kubectl to apply it:

```
kustomize build k8s/overlays/my-stackname | kubectl apply -f -
```

## Autoscaling

The example scales Buildkite agent pods using a [horizontal pod autoscaler](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/) and [buildkite metrics](https://github.com/elotl/buildscaler) from the default job queue. Whenever there are scheduled jobs waiting for execution the number of agent boxes scale up by either double or add 7 agents whichever is greater every 30 seconds. Whenever there are idle agent boxes they will begin to scale down 1 box every 20 seconds, but there may appear to be a delay if that box is currently running a job. These rules can be seen and modified in the supplied manifests.

## Running steps in a pod

The example uses the [Buildkite k8s job plugin](https://github.com/buildkite-plugins/k8s-job-buildkite-plugin) to allow running Buildkite pipeline jobs as a Kubenetes [Job](https://kubernetes.io/docs/concepts/workloads/controllers/job/) using a [Pod spec](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#PodSpec). Some example pipelines are included in this repository, as well as the source Dockerfiles for the associated containers. If you need to network between containers in a pod step you can use [kubedns](https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/) to talk between containers.

There are a couple of really simple test services -- [win penguin](https://github.com/buildkite/agent-stack-k8s) and [fail whale](https://github.com/buildkite/agent-stack-k8s). We also pushed the images for [win penguin](https://hub.docker.com/repository/docker/deftinc/winpenguin) and [fail whale](https://hub.docker.com/repository/docker/deftinc/failwhale) up to Docker Hub for testing.

## Running the stack locally

The easiest way to get started is to run a kind (kubernetes-in-docker) cluster on your local machine. We have a few scripts that make cluster provisioning and bootstrap easier.

You'll need to have the docker community edition installed to run the local tests with kind (kubernetes-in-docker). We recommend installing it directly from [https://www.docker.com/get-started/](https://www.docker.com/get-started/)

The rest of the local development environment dependencies are managed with [homebrew](https://brew.sh) and can be installed with the bootstrap script:

```
./bin/bootstrap
```

## Running the stack locally

The easiest way to get started is to run a kind(kubernetes-in-docker) cluster on your local machine. We have a few scripts that make cluster provisioning and bootstrap easier.

To get started run the command below. This will setup a single node kubernetes cluster in docker, add the kubernetes metric server, and then add manifests for the buildkite kubernetes stack.

```
./bin/up k8s/overlay/my-stackname
```

When you are finished tear it down with the command below. This deletes all the resources out of the cluster and

```
./bin/down k8s/overlay/my-stackname
```

## Known limitations

1. Build steps running in a pod spec are currently limited to checking the status code of the first container in the container list.
2. Agent scaling is pre-defined at the moment to show the functionality, but will be customizeable in the future.
3. The pod steps are not expected to run for more than 60 seconds within the pod. If they need longer to run adjust the timeout in the plugin configuration.
