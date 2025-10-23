package config

import (
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent/v3/version"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
)

const (
	UUIDLabel                             = "buildkite.com/job-uuid"
	ControllerIDLabel                     = "buildkite.com/controller-id"
	BuildURLAnnotation                    = "buildkite.com/build-url"
	BuildBranchAnnotation                 = "buildkite.com/build-branch"
	BuildRepoAnnotation                   = "buildkite.com/build-repo"
	JobURLAnnotation                      = "buildkite.com/job-url"
	PriorityAnnotation                    = "buildkite.com/job-priority"
	PipelineSlugAnnotation                = "buildkite.com/pipeline-slug"
	DefaultNamespace                      = "default"
	DefaultImagePullBackOffGracePeriod    = 30 * time.Second
	DefaultJobCancelCheckerPollInterval   = 5 * time.Second
	DefaultEmptyJobGracePeriod            = 30 * time.Second
	DefaultJobCreationConcurrency         = 25
	DefaultK8sClientRateLimiterQPS        = 10
	DefaultK8sClientRateLimiterBurst      = 20
	DefaultPaginationPageSize             = 1000
	DefaultPaginationDepthLimit           = 2
	DefaultQueryResetInterval             = 10 * time.Second
	DefaultTerminationGracePeriodSeconds  = 60
	DefaultWorkQueueLimit                 = 1_000_000
	DefaultImageCheckContainerCPULimit    = "200m"
	DefaultImageCheckContainerMemoryLimit = "128Mi"
)

var DefaultAgentImage = "ghcr.io/buildkite/agent:" + version.Version()

// viper requires mapstructure struct tags, but the k8s types only have json struct tags.
// mapstructure (the module) supports switching the struct tag to "json", viper does not. So we have
// to have the `mapstructure` tag for viper and the `json` tag is used by the mapstructure!
type Config struct {
	Debug bool `json:"debug"`

	// Job / Pod settings
	JobTTL                               time.Duration   `json:"job-ttl"`
	JobActiveDeadlineSeconds             int             `json:"job-active-deadline-seconds"              validate:"required"`
	JobPrefix                            string          `json:"job-prefix"                               validate:"required"`
	DefaultTerminationGracePeriodSeconds int             `json:"default-termination-grace-period-seconds" validate:"required"`
	Namespace                            string          `json:"namespace"                                validate:"required"`
	PodSpecPatch                         *corev1.PodSpec `json:"pod-spec-patch"                           validate:"omitempty"`

	// Controller settings
	JobCreationConcurrency int           `json:"job-creation-concurrency" validate:"omitempty"`
	AgentTokenSecret       string        `json:"agent-token-secret"       validate:"required"`
	Image                  string        `json:"image"                    validate:"required"`
	MaxInFlight            int           `json:"max-in-flight"            validate:"min=0"`
	Tags                   stringSlice   `json:"tags"`
	PrometheusPort         uint16        `json:"prometheus-port"          validate:"omitempty"`
	ProfilerAddress        string        `json:"profiler-address"         validate:"omitempty,hostname_port"`
	PollInterval           time.Duration `json:"poll-interval"`
	PaginationPageSize     int           `json:"pagination-page-size"     validate:"min=1,max=1000"`
	PaginationDepthLimit   int           `json:"pagination-depth-limit"   validate:"min=1,max=20"`
	QueryResetInterval     time.Duration `json:"query-reset-interval"     validate:"omitempty"`
	EnableQueuePause       bool          `json:"enable-queue-pause"       validate:"omitempty"`
	WorkQueueLimit         int           `json:"work-queue-limit"         validate:"omitempty"`
	// Agent endpoint is set in agent-config.

	// ID is an optional uniquely ID string for the controller.
	// This is useful when running multiple bk k8s controllers within the same k8s namespace.
	// So the controller can target the correct pods.
	// By default, if helm is used to install, this will be set as helm release full name.
	ID string `json:"id" validate:"omitempty"`

	K8sClientRateLimiterQPS   int `json:"k8s-client-rate-limiter-qps"   validate:"omitempty"`
	K8sClientRateLimiterBurst int `json:"k8s-client-rate-limiter-burst" validate:"omitempty"`

	ImagePullBackOffGracePeriod  time.Duration `json:"image-pull-backoff-grace-period"  validate:"omitempty"`
	JobCancelCheckerPollInterval time.Duration `json:"job-cancel-checker-poll-interval" validate:"omitempty"`
	EmptyJobGracePeriod          time.Duration `json:"empty-job-grace-period"           validate:"omitempty"`

	// WorkspaceVolume allows supplying a volume for /workspace. By default
	// an EmptyDir volume is created for it.
	WorkspaceVolume *corev1.Volume `json:"workspace-volume" validate:"omitempty"`

	AgentConfig            *AgentConfig    `json:"agent-config"             validate:"omitempty"`
	DefaultCheckoutParams  *CheckoutParams `json:"default-checkout-params"  validate:"omitempty"`
	DefaultCommandParams   *CommandParams  `json:"default-command-params"   validate:"omitempty"`
	DefaultSidecarParams   *SidecarParams  `json:"default-sidecar-params"   validate:"omitempty"`
	DefaultMetadata        Metadata        `json:"default-metadata"         validate:"omitempty"`
	AdditionalRedactedVars stringSlice     `json:"additional-redacted-vars" validate:"omitempty"`

	ResourceClasses map[string]*ResourceClass `json:"resource-classes" validate:"omitempty"`

	DefaultImagePullPolicy      corev1.PullPolicy `json:"default-image-pull-policy"       validate:"omitempty"`
	DefaultImageCheckPullPolicy corev1.PullPolicy `json:"default-image-check-pull-policy" validate:"omitempty"`

	SkipImageCheckContainers       bool   `json:"skip-image-check-containers"        validate:"omitempty"`
	ImageCheckContainerCPULimit    string `json:"image-check-container-cpu-limit"    validate:"omitempty"`
	ImageCheckContainerMemoryLimit string `json:"image-check-container-memory-limit" validate:"omitempty"`

	// ProhibitKubernetesPlugin can be used to prevent alterations to the pod
	// from the job (the kubernetes "plugin" in pipeline.yml). If enabled,
	// jobs with a "kubernetes" plugin will fail.
	ProhibitKubernetesPlugin bool `json:"prohibit-kubernetes-plugin" validate:"omitempty"`

	// AllowPodSpecPatchUnsafeCmdMod can be used to allow podSpecPatch to change
	// container commands. Normally this is prevented, because if the
	// replacement command does not execute buildkite-agent in the right way,
	// then the pod will malfunction.
	AllowPodSpecPatchUnsafeCmdMod bool `json:"allow-pod-spec-patch-unsafe-command-modification" validate:"omitempty"`

	// These are only used for integration tests.
	BuildkiteToken  string `json:"integration-test-buildkite-token"  validate:"omitempty"`
	GraphQLEndpoint string `json:"graphql-endpoint" validate:"omitempty"`
}

type stringSlice []string

func (s stringSlice) MarshalLogArray(enc zapcore.ArrayEncoder) error {
	for _, x := range s {
		enc.AppendString(x)
	}
	return nil
}

func (c Config) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("agent-token-secret", c.AgentTokenSecret)
	enc.AddBool("debug", c.Debug)
	enc.AddString("image", c.Image)
	enc.AddString("job-prefix", c.JobPrefix)
	enc.AddDuration("job-ttl", c.JobTTL)
	enc.AddInt("job-active-deadline-seconds", c.JobActiveDeadlineSeconds)
	enc.AddDuration("poll-interval", c.PollInterval)
	enc.AddInt("job-creation-concurrency", c.JobCreationConcurrency)
	enc.AddInt("max-in-flight", c.MaxInFlight)
	enc.AddString("namespace", c.Namespace)
	enc.AddString("id", c.ID)
	if err := enc.AddArray("tags", c.Tags); err != nil {
		return err
	}
	enc.AddString("profiler-address", c.ProfilerAddress)
	enc.AddUint16("prometheus-port", c.PrometheusPort)
	enc.AddBool("prohibit-kubernetes-plugin", c.ProhibitKubernetesPlugin)
	enc.AddBool("allow-pod-spec-patch-unsafe-command-modification", c.AllowPodSpecPatchUnsafeCmdMod)
	if err := enc.AddArray("additional-redacted-vars", c.AdditionalRedactedVars); err != nil {
		return err
	}
	if err := enc.AddReflected("pod-spec-patch", c.PodSpecPatch); err != nil {
		return err
	}
	enc.AddDuration("image-pull-backoff-grace-period", c.ImagePullBackOffGracePeriod)
	enc.AddDuration("job-cancel-checker-poll-interval", c.JobCancelCheckerPollInterval)
	if err := enc.AddReflected("agent-config", c.AgentConfig); err != nil {
		return err
	}
	if err := enc.AddReflected("default-checkout-params", c.DefaultCheckoutParams); err != nil {
		return err
	}
	if err := enc.AddReflected("default-command-params", c.DefaultCommandParams); err != nil {
		return err
	}
	if err := enc.AddReflected("default-sidecar-params", c.DefaultSidecarParams); err != nil {
		return err
	}
	if err := enc.AddReflected("default-metadata", c.DefaultMetadata); err != nil {
		return err
	}
	enc.AddString("default-image-pull-policy", string(c.DefaultImagePullPolicy))
	enc.AddString("default-image-check-pull-policy", string(c.DefaultImageCheckPullPolicy))
	enc.AddBool("enable-queue-pause", c.EnableQueuePause)
	enc.AddInt("pagination-page-size", c.PaginationPageSize)
	enc.AddInt("pagination-depth-limit", c.PaginationDepthLimit)
	enc.AddDuration("query-reset-interval", c.QueryResetInterval)
	enc.AddInt("work-queue-limit", c.WorkQueueLimit)
	if err := enc.AddReflected("resource-classes", c.ResourceClasses); err != nil {
		return err
	}
	return nil
}

// Helpers for applying configs / params to container env.

func appendToEnv(ctr *corev1.Container, name, value string) {
	ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: value})
}

func appendToEnvOpt(ctr *corev1.Container, name string, value *string) {
	if value == nil {
		return
	}
	ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: *value})
}

func appendBoolToEnvOpt(ctr *corev1.Container, name string, value *bool) {
	if value == nil {
		return
	}
	ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: strconv.FormatBool(*value)})
}

func appendNegatedToEnvOpt(ctr *corev1.Container, name string, value *bool) {
	if value == nil {
		return
	}
	ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: strconv.FormatBool(!*value)})
}

func appendCommaSepToEnv(ctr *corev1.Container, name string, values []string) {
	if len(values) == 0 {
		return
	}
	ctr.Env = append(ctr.Env, corev1.EnvVar{
		Name:  name,
		Value: strings.Join(values, ","),
	})
}

// Iterates over Containers in PodSpec to deduplicate VolumeMounts
func PrepareVolumeMounts(ctrSpec []corev1.Container) []corev1.Container {
	for ctr := range ctrSpec {
		var filteredMounts []corev1.VolumeMount

		for _, mount := range ctrSpec[ctr].VolumeMounts {
			uniqueMount := true
			for _, filteredMount := range filteredMounts {
				if mount.MountPath == filteredMount.MountPath {
					uniqueMount = false
				}
			}
			if uniqueMount {
				filteredMounts = append(filteredMounts, mount)
			}
		}
		ctrSpec[ctr].VolumeMounts = filteredMounts
	}
	return ctrSpec
}
