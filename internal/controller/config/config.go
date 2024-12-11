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
	UUIDLabel                           = "buildkite.com/job-uuid"
	BuildURLAnnotation                  = "buildkite.com/build-url"
	JobURLAnnotation                    = "buildkite.com/job-url"
	DefaultNamespace                    = "default"
	DefaultStaleJobDataTimeout          = 10 * time.Second
	DefaultImagePullBackOffGracePeriod  = 30 * time.Second
	DefaultJobCancelCheckerPollInterval = 5 * time.Second
	DefaultEmptyJobGracePeriod          = 30 * time.Second
	DefaultJobCreationConcurrency       = 5
)

var DefaultAgentImage = "ghcr.io/buildkite/agent:" + version.Version()

// viper requires mapstructure struct tags, but the k8s types only have json struct tags.
// mapstructure (the module) supports switching the struct tag to "json", viper does not. So we have
// to have the `mapstructure` tag for viper and the `json` tag is used by the mapstructure!
type Config struct {
	Debug                  bool          `json:"debug"`
	JobTTL                 time.Duration `json:"job-ttl"`
	PollInterval           time.Duration `json:"poll-interval"`
	StaleJobDataTimeout    time.Duration `json:"stale-job-data-timeout"   validate:"omitempty"`
	JobCreationConcurrency int           `json:"job-creation-concurrency" validate:"omitempty"`
	AgentTokenSecret       string        `json:"agent-token-secret"       validate:"required"`
	BuildkiteToken         string        `json:"buildkite-token"          validate:"required"`
	Image                  string        `json:"image"                    validate:"required"`
	MaxInFlight            int           `json:"max-in-flight"            validate:"min=0"`
	Namespace              string        `json:"namespace"                validate:"required"`
	Org                    string        `json:"org"                      validate:"required"`
	Tags                   stringSlice   `json:"tags"                     validate:"min=1"`
	PrometheusPort         uint16        `json:"prometheus-port"          validate:"omitempty"`
	ProfilerAddress        string        `json:"profiler-address"         validate:"omitempty,hostname_port"`
	GraphQLEndpoint        string        `json:"graphql-endpoint"         validate:"omitempty"`
	// Agent endpoint is set in agent-config.

	// ClusterUUID field is mandatory for most new orgs.
	// Some old orgs allows unclustered setup.
	ClusterUUID                  string          `json:"cluster-uuid"                     validate:"omitempty"`
	AdditionalRedactedVars       stringSlice     `json:"additional-redacted-vars"         validate:"omitempty"`
	PodSpecPatch                 *corev1.PodSpec `json:"pod-spec-patch"                   validate:"omitempty"`
	ImagePullBackOffGracePeriod  time.Duration   `json:"image-pull-backoff-grace-period"  validate:"omitempty"`
	JobCancelCheckerPollInterval time.Duration   `json:"job-cancel-checker-poll-interval" validate:"omitempty"`
	EmptyJobGracePeriod          time.Duration   `json:"empty-job-grace-period"           validate:"omitempty"`

	// WorkspaceVolume allows supplying a volume for /workspace. By default
	// an EmptyDir volume is created for it.
	WorkspaceVolume *corev1.Volume `json:"workspace-volume" validate:"omitempty"`

	AgentConfig           *AgentConfig    `json:"agent-config"            validate:"omitempty"`
	DefaultCheckoutParams *CheckoutParams `json:"default-checkout-params" validate:"omitempty"`
	DefaultCommandParams  *CommandParams  `json:"default-command-params"  validate:"omitempty"`
	DefaultSidecarParams  *SidecarParams  `json:"default-sidecar-params"  validate:"omitempty"`
	DefaultMetadata       Metadata        `json:"default-metadata"        validate:"omitempty"`

	// ProhibitKubernetesPlugin can be used to prevent alterations to the pod
	// from the job (the kubernetes "plugin" in pipeline.yml). If enabled,
	// jobs with a "kubernetes" plugin will fail.
	ProhibitKubernetesPlugin bool `json:"prohibit-kubernetes-plugin" validate:"omitempty"`

	// AllowPodSpecPatchRawCmdMod can be used to allow podSpecPatch to change
	// container commands. Normally this is prevented, because if the
	// replacement command does not execute buildkite-agent in the right way,
	// then the pod will malfunction.
	AllowPodSpecPatchRawCmdMod bool `json:"allow-pod-spec-patch-raw-command-modification" validate:"omitempty"`
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
	enc.AddDuration("job-ttl", c.JobTTL)
	enc.AddDuration("poll-interval", c.PollInterval)
	enc.AddDuration("stale-job-data-timeout", c.StaleJobDataTimeout)
	enc.AddInt("job-creation-concurrency", c.JobCreationConcurrency)
	enc.AddInt("max-in-flight", c.MaxInFlight)
	enc.AddString("namespace", c.Namespace)
	enc.AddString("org", c.Org)
	if err := enc.AddArray("tags", c.Tags); err != nil {
		return err
	}
	enc.AddString("profiler-address", c.ProfilerAddress)
	enc.AddUint16("prometheus-port", c.PrometheusPort)
	enc.AddString("cluster-uuid", c.ClusterUUID)
	enc.AddBool("prohibit-kubernetes-plugin", c.ProhibitKubernetesPlugin)
	enc.AddBool("allow-pod-spec-patch-raw-command-modification", c.AllowPodSpecPatchRawCmdMod)
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
