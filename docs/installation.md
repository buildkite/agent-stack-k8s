# Installation

[TOC]
    -   [Requirements](#requirements)
    -   [Deploy with Helm](#deploy-with-helm)
    -   [Options](#options)
    -   [Buildkite cluster's UUID](#buildkite-clusters-uuid) 

## Requirements

- A Kubernetes cluster
- An API token with the [GraphQL scope enabled](https://buildkite.com/docs/apis/graphql-api#authentication)
- An [agent token](https://buildkite.com/docs/agent/v3/tokens)
- A Buildkite [cluster's UUID](#buildkite-clusters-uuid)

## Deploy with Helm

You'll need Helm version 3.8.0 or newer since we're using Helm's support for [OCI-based registries](https://helm.sh/docs/topics/registries/).

The simplest way to get up and running is by deploying our [Helm](https://helm.sh) chart:

```bash
helm upgrade --install agent-stack-k8s oci://ghcr.io/buildkite/helm/agent-stack-k8s \
    --create-namespace \
    --namespace buildkite \
    --set config.org=<your Buildkite org slug> \
    --set agentToken=<your Buildkite agent token> \
    --set graphqlToken=<your Buildkite GraphQL-enabled API token> \
    --set config.cluster-uuid=<your Buildkite cluster's UUID>
```
This will create an agent-stack-k8s installation that will listen to the `kubernetes` queue.

See the `--tags` [option](#Options) for specifying a different queue. 

See [here](#buildkite-clusters-uuid) for more info on the cluster's UUID.

## Options

```text
Usage:
  agent-stack-k8s [flags]
  agent-stack-k8s [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  lint        A tool for linting Buildkite pipelines
  version     Prints the version

Flags:
      --agent-token-secret string                   name of the Buildkite agent token secret (default "buildkite-agent-token")
      --buildkite-token string                      Buildkite API token with GraphQL scopes
      --cluster-uuid string                         UUID of the Buildkite Cluster. The agent token must be for the Buildkite Cluster.
  -f, --config string                               config file path
      --debug                                       debug logs
      --default-image-check-pull-policy string      Sets a default PullPolicy for image-check init containers, used if an image pull policy is not set for the corresponding container in a podSpec or podSpecPatch
      --default-image-pull-policy string            Configures a default image pull policy for containers that do not specify a pull policy and non-init containers created by the stack itself (default "IfNotPresent")
      --empty-job-grace-period duration             Duration after starting a Kubernetes job that the controller will wait before considering failing the job due to a missing pod (e.g. when the podSpec specifies a missing service account) (default 30s)
      --graphql-endpoint string                     Buildkite GraphQL endpoint URL
      --graphql-results-limit int                   Sets the amount of results returned by GraphQL queries when retrieving Jobs to be Scheduled (default 100)
  -h, --help                                        help for agent-stack-k8s
      --image string                                The image to use for the Buildkite agent (default "ghcr.io/buildkite/agent:3.91.0")
      --image-pull-backoff-grace-period duration    Duration after starting a pod that the controller will wait before considering cancelling a job due to ImagePullBackOff (e.g. when the podSpec specifies container images that cannot be pulled) (default 30s)
      --job-cancel-checker-poll-interval duration   Controls the interval between job state queries while a pod is still Pending (default 5s)
      --job-creation-concurrency int                Number of concurrent goroutines to run for converting Buildkite jobs into Kubernetes jobs (default 5)
      --job-ttl duration                            time to retain kubernetes jobs after completion (default 10m0s)
      --job-active-deadline-seconds int             maximum number of seconds a kubernetes job is allowed to run before terminating all pods and failing (default 21600)
      --k8s-client-rate-limiter-burst int           The burst value of the K8s client rate limiter. (default 20)
      --k8s-client-rate-limiter-qps int             The QPS value of the K8s client rate limiter. (default 10)
      --max-in-flight int                           max jobs in flight, 0 means no max (default 25)
      --namespace string                            kubernetes namespace to create resources in (default "default")
      --org string                                  Buildkite organization name to watch
      --pagination-depth-limit int                  Sets the maximum depth of pagination when retreiving Buildkite Jobs to be Scheduled. Increasing this value will increase the number of requests made to the Buildkite GraphQL API and number of Jobs to be scheduled on the Kubernetes Cluster. (default 1)
      --poll-interval duration                      time to wait between polling for new jobs (minimum 1s); note that increasing this causes jobs to be slower to start (default 1s)
      --profiler-address string                     Bind address to expose the pprof profiler (e.g. localhost:6060)
      --prohibit-kubernetes-plugin                  Causes the controller to prohibit the kubernetes plugin specified within jobs (pipeline YAML) - enabling this causes jobs with a kubernetes plugin to fail, preventing the pipeline YAML from having any influence over the podSpec
      --prometheus-port uint16                      Bind port to expose Prometheus /metrics; 0 disables it
      --stale-job-data-timeout duration             Duration after querying jobs in Buildkite that the data is considered valid (default 10s)
      --tags strings                                A comma-separated list of agent tags. The "queue" tag must be unique (e.g. "queue=kubernetes,os=linux") (default [queue=kubernetes])
      --enable-queue-pause bool                     Allow the controller to pause processing the jobs when the queue is paused on Buildkite. (default false)


Use "agent-stack-k8s [command] --help" for more information about a command.
```

Configuration can also be provided by a config file (`--config` or `CONFIG`), or environment variables. In the [examples](examples) folder there is a sample [YAML config](examples/config.yaml) and a sample [dotenv config](examples/config.env).

With release v0.24.0 of `agent-stack-k8s`, we can enable '-enable-queue-pause` in the config, allowing the controller to pause processing the jobs when `queue` is paused on Buildkite.

### Buildkite Cluster's UUID

With the introduction of [Buildkite Clusters](https://buildkite.com/docs/agent/clusters) in 2024, it's now required to specify your cluster's UUID in the configuration for the controller when you deploy with Helm.

To find the cluster's UUID, go to the [Clusters page](https://buildkite.com/organizations/-/clusters), click on the relevant cluster, and click on "Settings". The cluster's UUID will be in the section titled "GraphQL API Integration".

You can specify your cluster's UUID by either:
 
- Setting a flag on the `helm` command like described earlier: 
`--set config.cluster-uuid=<your cluster's UUID>` 

- Or adding an entry in your `values.yaml` file:
```yaml
# values.yaml
config:
  cluster-uuid: beefcafe-abbe-baba-abba-deedcedecade
```

### Externalize Secrets

You can also have an external provider create a secret for you in the namespace before deploying the chart with Helm. If the secret is pre-provisioned, replace the `agentToken` and `graphqlToken` arguments with:

```bash
--set agentStackSecret=<secret-name>
```

The format of the required secret can be found in [this file](./charts/agent-stack-k8s/templates/secrets.yaml.tpl).

### Other Installation Methods

You can also use this chart as a dependency:

```yaml
dependencies:
- name: agent-stack-k8s
  version: "0.5.0"
  repository: "oci://ghcr.io/buildkite/helm"
```

or use it as a template:

```
helm template oci://ghcr.io/buildkite/helm/agent-stack-k8s -f my-values.yaml
```

Available versions and their digests can be found on [the releases page](https://github.com/buildkite/agent-stack-k8s/releases).