package controller

import (
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/spf13/cobra"
)

func AddConfigFlags(cmd *cobra.Command) {
	// the config file flag
	cmd.Flags().StringVarP(&configFile, "config", "f", "", "config file path")

	// not in the config file
	cmd.Flags().String(
		"agent-token-secret",
		"buildkite-agent-token",
		"name of the Buildkite agent token secret",
	)
	cmd.Flags().String("buildkite-token", "", "Deprecated - Buildkite API token with GraphQL scopes")

	// in the config file
	cmd.Flags().String(
		"image",
		config.DefaultAgentImage,
		"The image to use for the Buildkite agent",
	)
	cmd.Flags().String(
		"job-prefix",
		"buildkite-",
		"The prefix to use when creating Kubernetes job names",
	)
	cmd.Flags().StringSlice(
		"tags",
		[]string{},
		`A comma-separated list of agent tags. The "queue" tag must be unique (e.g. "queue=kubernetes,os=linux")`,
	)
	cmd.Flags().String(
		"namespace",
		config.DefaultNamespace,
		"kubernetes namespace to create resources in",
	)
	cmd.Flags().Bool("debug", false, "debug logs")
	cmd.Flags().Int("max-in-flight", 25, "max jobs in flight, 0 means no max")
	cmd.Flags().Duration(
		"job-ttl",
		10*time.Minute,
		"time to retain kubernetes jobs after completion",
	)
	cmd.Flags().Int(
		"job-active-deadline-seconds",
		21600,
		"maximum number of seconds a kubernetes job is allowed to run before terminating all pods and failing job",
	)
	cmd.Flags().Int(
		"default-termination-grace-period-seconds",
		config.DefaultTerminationGracePeriodSeconds,
		"maximum number of seconds a pod will run after being told to terminate, if not otherwise set by podSpec",
	)
	cmd.Flags().Duration(
		"poll-interval",
		time.Second,
		"time to wait between polling for new jobs (minimum 1s); note that increasing this causes jobs to be slower to start",
	)
	cmd.Flags().String(
		"cluster-uuid",
		"",
		"UUID of the Buildkite Cluster. The agent token must be for the Buildkite Cluster.",
	)
	cmd.Flags().String(
		"profiler-address",
		"",
		"Bind address to expose the pprof profiler (e.g. localhost:6060)",
	)
	cmd.Flags().Uint16(
		"prometheus-port",
		0,
		"Bind port to expose Prometheus /metrics; 0 disables it",
	)
	cmd.Flags().String("graphql-endpoint", "", "Deprecated - Buildkite GraphQL endpoint URL")

	cmd.Flags().Int(
		"job-creation-concurrency",
		config.DefaultJobCreationConcurrency,
		"Number of concurrent goroutines to run for converting Buildkite jobs into Kubernetes jobs",
	)
	cmd.Flags().Int(
		"k8s-client-rate-limiter-qps",
		config.DefaultK8sClientRateLimiterQPS,
		"The QPS value of the K8s client rate limiter.",
	)
	cmd.Flags().Int(
		"k8s-client-rate-limiter-burst",
		config.DefaultK8sClientRateLimiterBurst,
		"The burst value of the K8s client rate limiter.",
	)
	cmd.Flags().Duration(
		"image-pull-backoff-grace-period",
		config.DefaultImagePullBackOffGracePeriod,
		"Duration after starting a pod that the controller will wait before considering cancelling a job due to ImagePullBackOff (e.g. when the podSpec specifies container images that cannot be pulled)",
	)
	cmd.Flags().Duration(
		"job-cancel-checker-poll-interval",
		config.DefaultJobCancelCheckerPollInterval,
		"Controls the interval between job state queries while a pod is still Pending",
	)
	cmd.Flags().Duration(
		"empty-job-grace-period",
		config.DefaultEmptyJobGracePeriod,
		"Duration after starting a Kubernetes job that the controller will wait before considering failing the job due to a missing pod (e.g. when the podSpec specifies a missing service account)",
	)
	cmd.Flags().String(
		"default-image-pull-policy",
		"",
		"Configures a default image pull policy for containers that do not specify a pull policy and non-init containers created by the stack itself",
	)
	cmd.Flags().String(
		"default-image-check-pull-policy",
		"",
		"Sets a default PullPolicy for image-check init containers, used if an image pull policy is not set for the corresponding container in a podSpec or podSpecPatch",
	)
	cmd.Flags().Bool(
		"prohibit-kubernetes-plugin",
		false,
		"Causes the controller to prohibit the kubernetes plugin specified within jobs (pipeline YAML) - enabling this causes jobs with a kubernetes plugin to fail, preventing the pipeline YAML from having any influence over the podSpec",
	)
	cmd.Flags().Bool(
		"enable-queue-pause",
		false,
		"Allow controller to pause processing the jobs when queue is paused on Buildkite",
	)
	cmd.Flags().Bool(
		"allow-pod-spec-patch-unsafe-command-modification",
		false,
		"Permits PodSpecPatch to modify the command or args fields of stack-provided containers. See the warning in the README before enabling this option",
	)
	cmd.Flags().Bool(
		"experimental-job-reservation-support",
		false,
		"Experimental - does not fully function yet. This experiment enables job reservation support for better job observability and scalable job fetching. If you try it, please let us know about your experiences by filing an issue on https://github.com/buildkite/agent-stack-k8s",
	)
	cmd.Flags().Bool(
		"experimental-stacks-api-support",
		false,
		"Experimental - enables integration with the Buildkite Stacks API for interactions with the Buildkite control plane.",
	)
	cmd.Flags().Int(
		"pagination-page-size",
		config.DefaultPaginationPageSize,
		"Sets the maximum number of Jobs per page when retrieving Buildkite Jobs to be Scheduled.",
	)
	cmd.Flags().Int(
		"pagination-depth-limit",
		config.DefaultPaginationDepthLimit,
		"Sets the maximum number of pages when retrieving Buildkite Jobs to be Scheduled. Increasing this value will increase the number of requests made to the Buildkite API and number of Jobs to be scheduled on the Kubernetes Cluster.",
	)
	cmd.Flags().Duration(
		"query-reset-interval",
		config.DefaultQueryResetInterval,
		"Controls the interval between pagination cursor resets. Increasing this value will increase the number of jobs to be scheduled but also delay picking up any jobs that were missed from the start of the query.",
	)
	cmd.Flags().Int(
		"work-queue-limit",
		config.DefaultWorkQueueLimit,
		"Sets the maximum number of Jobs the controller will hold in the work queue.",
	)
	cmd.Flags().Bool(
		"skip-image-check-containers",
		false,
		"Disable and skip all imagecheck-* init containers",
	)
	cmd.Flags().String(
		"image-check-container-cpu-limit",
		config.DefaultImageCheckContainerCPULimit,
		"Configures the CPU resource limits for all imagecheck-* containers",
	)
	cmd.Flags().String(
		"image-check-container-memory-limit",
		config.DefaultImageCheckContainerMemoryLimit,
		"Configures the memory resource limits for all imagecheck-* containers",
	)
}
