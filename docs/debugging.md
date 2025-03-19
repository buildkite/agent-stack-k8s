# Debugging

You can enable debug logging via the command line (`--debug`) or within the `values.yaml` file (`debug: true`)

Use the `log-collector` script in the `utils` folder to collect logs for agent-stack-k8s.

## Prerequisites

- kubectl binary
- kubectl setup and authenticated to correct k8s cluster

## Inputs to the script

- Kubernetes namespace where you deployed Agent Stack Kubernetes and where you expect the Kubernetes jobs to run
- [Buildkite job ID](https://buildkite.com/docs/agent/v3/cli-start#run-a-single-job-getting-the-job-id-for-a-single-job) for the job in which you saw issues

## Data/logs gathered

A script will collect kubectl describe of a Kubernetes job, pod, and Agent Stack Kubernetes controller pod.

It will also capture kubectl logs of Kubernetes pod for the Buildkite job, Agent Stack Kubernetes controller pod and package them in a
tar archive which you can send via email to support@buildkite.com.
