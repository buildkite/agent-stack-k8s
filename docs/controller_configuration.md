# Controller Configuration

* [Command Line Arguments](#command-line-arguments)
* [Kubernetes Node Selection](#kubernetes-node-selection)

## Command Line Arguments

### Usage

- `agent-stack-k8s [command]`
- `agent-stack-k8s [flags]`

### Available Commands

| Command     | Description                                                       |
|-------------|-------------------------------------------------------------------|
| `completion`| Generate the autocompletion script for the specified shell        |
| `help`      | Help about any command                                            |
| `lint`      | A tool for linting Buildkite pipelines                            |
| `version`   | Prints the version                                                |

Use `agent-stack-k8s [command] --help` for more information about a command.

### Flags

| Flag                                           | Description                                                                                                                                                                                                                                                                                                                   |
|------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--agent-token-secret string`                  | name of the Buildkite agent token secret (default "buildkite-agent-token")                                                                                                                                                                                                                                                    |
| `--buildkite-token string`                     | Buildkite API token with GraphQL scopes                                                                                                                                                                                                                                                                                       |
| `--cluster-uuid string`                        | UUID of the Buildkite Cluster. The agent token must be for the Buildkite Cluster.                                                                                                                                                                                                                                             |
| `-f, --config string`                          | config file path                                                                                                                                                                                                                                                                                                              |
| `--debug`                                      | debug logs                                                                                                                                                                                                                                                                                                                    |
| `--default-image-check-pull-policy string`     | Sets a default PullPolicy for image-check init containers, used if an image pull policy is not set for the corresponding container in a podSpec or podSpecPatch                                                                                                                                                               |
| `--default-image-pull-policy string`           | Configures a default image pull policy for containers that do not specify a pull policy and non-init containers created by the stack itself (default "IfNotPresent")                                                                                                                                                          |
| `--empty-job-grace-period duration`            | Duration after starting a Kubernetes job that the controller will wait before considering failing the job due to a missing pod (e.g., when the podSpec specifies a missing service account) (default 30s)                                                                                                                     |
| `--enable-queue-pause bool`                    | Allow the controller to pause processing the jobs when the queue is paused on Buildkite (requires `v0.24.0` or greater) (default false)                                                                                                                                                                                                                      |
| `--graphql-endpoint string`                    | Buildkite GraphQL endpoint URL                                                                                                                                                                                                                                                                                                |
| `--graphql-results-limit int`                  | Sets the amount of results returned by GraphQL queries when retrieving Jobs to be Scheduled (default 100)                                                                                                                                                                                                                     |
| `-h, --help`                                   | help for agent-stack-k8s                                                                                                                                                                                                                                                                                                      |
| `--image string`                               | The image to use for the Buildkite agent (default "ghcr.io/buildkite/agent:3.91.0")                                                                                                                                                                                                                                           |
| `--image-pull-backoff-grace-period duration`   | Duration after starting a pod that the controller will wait before considering cancelling a job due to ImagePullBackOff (e.g., when the podSpec specifies container images that cannot be pulled) (default 30s)                                                                                                               |
| `--job-cancel-checker-poll-interval duration`  | Controls the interval between job state queries while a pod is still Pending (default 5s)                                                                                                                                                                                                                                     |
| `--job-creation-concurrency int`               | Number of concurrent goroutines to run for converting Buildkite jobs into Kubernetes jobs (default 25)                                                                                                                                                                                                                        |
| `--job-ttl duration`                           | time to retain kubernetes jobs after completion (default 10m0s)                                                                                                                                                                                                                                                               |
| `--job-active-deadline-seconds int`            | maximum number of seconds a kubernetes job is allowed to run before terminating all pods and failing (default 21600)                                                                                                                                                                                                          |
| `--k8s-client-rate-limiter-burst int`          | The burst value of the K8s client rate limiter. (default 20)                                                                                                                                                                                                                                                                  |
| `--k8s-client-rate-limiter-qps int`            | The QPS value of the K8s client rate limiter. (default 10)                                                                                                                                                                                                                                                                    |
| `--max-in-flight int`                          | max jobs in flight, 0 means no max (default 25)                                                                                                                                                                                                                                                                               |
| `--namespace string`                           | kubernetes namespace to create resources in (default "default")                                                                                                                                                                                                                                                               |
| `--org string`                                 | Buildkite organization name to watch                                                                                                                                                                                                                                                                                          |
| `--pagination-depth-limit int`                 | Sets the maximum number of pages when retrieving Buildkite Jobs to be Scheduled. Increasing this value will increase the number of requests made to the Buildkite API and number of Jobs to be scheduled on the Kubernetes Cluster. (default 2)                                                                               |
| `--pagination-page-size int`                   | Sets the maximum number of Jobs per page when retrieving Buildkite Jobs to be Scheduled. (default 1000)                                                                                                                                                                                                                       |
| `--poll-interval duration`                     | time to wait between polling for new jobs (minimum 1s); note that increasing this causes jobs to be slower to start (default 1s)                                                                                                                                                                                              |
| `--profiler-address string`                    | Bind address to expose the pprof profiler (e.g., localhost:6060)                                                                                                                                                                                                                                                              |
| `--prohibit-kubernetes-plugin`                 | Causes the controller to prohibit the kubernetes plugin specified within jobs (pipeline YAML) - enabling this causes jobs with a kubernetes plugin to fail, preventing the pipeline YAML from having any influence over the podSpec                                                                                           |
| `--prometheus-port uint16`                     | Bind port to expose Prometheus /metrics; 0 disables it                                                                                                                                                                                                                                                                        |
| `--query-reset-interval duration`              | Controls the interval between pagination cursor resets. Increasing this value will increase the number of jobs to be scheduled but also delay picking up any jobs that were missed from the start of the query. (default 10s)                                                                                                 |
| `--tags strings`                               | A comma-separated list of agent tags. The "queue" tag must be unique (e.g., "queue=kubernetes,os=linux") (default [queue=kubernetes])                                                                                                                                                                                         |

## Kubernetes Node Selection

The `agent-stack-k8s` controller can be deployed to particular Kubernetes Nodes, using the Kubernetes PodSpec [`nodeSelector`](https://kubernetes.io/docs/tasks/configure-pod-container/assign-pods-nodes/#create-a-pod-that-gets-scheduled-to-your-chosen-node) field.

### Configuration values YAML file

The `nodeSelector` field can be defined in the controller's configuration:

```yaml
# values.yml
...
nodeSelector:
  teamowner: "services"
config:
...
```

## Additional Environment Variables for `agent-stack-k8s` Controller Container

If the `agent-stack-k8s` controller container requires extra environment variables in order to correctly operate inside your Kubernetes cluster, they can be added to your values YAML file and applied during a deployment with Helm.

### Configuration values YAML file

The `controllerEnv` field can be used to define extra Kubernetes EnvVar environment variables that will apply to the `agent-stack-k8s` controller container:

```yaml
# values.yml
...
controllerEnv:
  - name: KUBERNETES_SERVICE_HOST
    value: "10.10.10.10"
  - name: KUBERNETES_SERVICE_PORT
    value: "8443"
config:
...
```
