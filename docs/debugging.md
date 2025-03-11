# Debugging

Enable debug logging via the command line (`--debug`) or within the `values.yaml` file (`debug: true`)

Use the `log-collector` script in the `utils` folder to collect logs for agent-stack-k8s.

## Prerequisites

- kubectl binary
- kubectl setup and authenticated to correct k8s cluster

## Inputs to the script

k8s namespace where you deployed agent stack k8s and where you expect their k8s jobs to run.

Buildkite job id for which you saw issues.

## Data/logs gathered:

The script will collect kubectl describe of k8s job, pod and agent stack k8s controller pod.

It will also capture kubectl logs of k8s pod for the Buildkite job, agent stack k8s controller pod and package them in a
tar archive which you can send via email to support@buildkite.com.
