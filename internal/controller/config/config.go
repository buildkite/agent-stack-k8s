package config

import (
	"time"

	"github.com/buildkite/agent/v3/version"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
)

const (
	UUIDLabel                          = "buildkite.com/job-uuid"
	BuildURLAnnotation                 = "buildkite.com/build-url"
	JobURLAnnotation                   = "buildkite.com/job-url"
	DefaultNamespace                   = "default"
	DefaultImagePullBackOffGracePeriod = 30 * time.Second
)

var DefaultAgentImage = "ghcr.io/buildkite/agent:" + version.Version()

// viper requires mapstructure struct tags, but the k8s types only have json struct tags.
// mapstructure (the module) supports switching the struct tag to "json", viper does not. So we have
// to have the `mapstructure` tag for viper and the `json` tag is used by the mapstructure!
type Config struct {
	Debug            bool          `json:"debug"`
	JobTTL           time.Duration `json:"job-ttl"`
	PollInterval     time.Duration `json:"poll-interval"`
	AgentTokenSecret string        `json:"agent-token-secret"              validate:"required"`
	BuildkiteToken   string        `json:"buildkite-token"                 validate:"required"`
	Image            string        `json:"image"                           validate:"required"`
	MaxInFlight      int           `json:"max-in-flight"                   validate:"min=0"`
	Namespace        string        `json:"namespace"                       validate:"required"`
	Org              string        `json:"org"                             validate:"required"`
	Tags             stringSlice   `json:"tags"                            validate:"min=1"`
	ProfilerAddress  string        `json:"profiler-address"                validate:"omitempty,hostname_port"`
	// This field is mandatory for most new orgs.
	// Some old orgs allows unclustered setup.
	ClusterUUID                 string          `json:"cluster-uuid"                    validate:"omitempty"`
	AdditionalRedactedVars      stringSlice     `json:"additional-redacted-vars"        validate:"omitempty"`
	PodSpecPatch                *corev1.PodSpec `json:"pod-spec-patch"                  validate:"omitempty"`
	ImagePullBackOffGradePeriod time.Duration   `json:"image-pull-backoff-grace-period" validate:"omitempty"`
	DefaultCheckoutParams       *CheckoutParams `json:"default-checkout-params"         validate:"omitempty"`
	DefaultCommandParams        *CommandParams  `json:"default-command-params"          validate:"omitempty"`
	DefaultSidecarParams        *SidecarParams  `json:"default-sidecar-params"          validate:"omitempty"`

	// ProhibitKubernetesPlugin can be used to prevent alterations to the pod
	// from the job (the kubernetes "plugin" in pipeline.yml). If enabled,
	// jobs with a "kubernetes" plugin will fail.
	ProhibitKubernetesPlugin bool `json:"prohibit-kubernetes-plugin" validate:"omitempty"`
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
	enc.AddInt("max-in-flight", c.MaxInFlight)
	enc.AddString("namespace", c.Namespace)
	enc.AddString("org", c.Org)
	enc.AddString("profiler-address", c.ProfilerAddress)
	enc.AddString("cluster-uuid", c.ClusterUUID)
	enc.AddBool("prohibit-kubernetes-plugin", c.ProhibitKubernetesPlugin)
	if err := enc.AddArray("additional-redacted-vars", c.AdditionalRedactedVars); err != nil {
		return err
	}
	if err := enc.AddReflected("pod-spec-patch", c.PodSpecPatch); err != nil {
		return err
	}
	enc.AddDuration("image-pull-backoff-grace-period", c.ImagePullBackOffGradePeriod)
	if err := enc.AddReflected("default-checkout-params", c.DefaultCheckoutParams); err != nil {
		return err
	}
	if err := enc.AddReflected("default-command-params", c.DefaultCommandParams); err != nil {
		return err
	}
	if err := enc.AddReflected("default-sidecar-params", c.DefaultSidecarParams); err != nil {
		return err
	}
	return enc.AddArray("tags", c.Tags)
}

// CommandParams contains parameters that provide additional control over all command
// container(s).
type CommandParams struct {
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

func (cmd *CommandParams) ApplyTo(ctr *corev1.Container) {
	if cmd == nil || ctr == nil {
		return
	}
	ctr.EnvFrom = append(ctr.EnvFrom, cmd.EnvFrom...)
}

// SidecarParams contains parameters that provide additional control over all sidecar
// container(s).
type SidecarParams struct {
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

func (sc *SidecarParams) ApplyTo(ctr *corev1.Container) {
	if sc == nil || ctr == nil {
		return
	}
	ctr.EnvFrom = append(ctr.EnvFrom, sc.EnvFrom...)
}

// CheckoutParams contains parameters that provide additional control over the
// checkout container.
type CheckoutParams struct {
	Skip                 *bool                      `json:"skip,omitempty"`
	CloneFlags           *string                    `json:"cloneFlags,omitempty"`
	FetchFlags           *string                    `json:"fetchFlags,omitempty"`
	GitCredentialsSecret *corev1.SecretVolumeSource `json:"gitCredentialsSecret,omitempty"`
	EnvFrom              []corev1.EnvFromSource     `json:"envFrom,omitempty"`
}

func (co *CheckoutParams) ApplyTo(ctr *corev1.Container) {
	if co == nil || ctr == nil {
		return
	}
	if co.CloneFlags != nil {
		ctr.Env = append(ctr.Env, corev1.EnvVar{
			Name:  "BUILDKITE_GIT_CLONE_FLAGS",
			Value: *co.CloneFlags,
		})
	}
	if co.FetchFlags != nil {
		ctr.Env = append(ctr.Env, corev1.EnvVar{
			Name:  "BUILDKITE_GIT_FETCH_FLAGS",
			Value: *co.FetchFlags,
		})
	}
	ctr.EnvFrom = append(ctr.EnvFrom, co.EnvFrom...)
}

func (co *CheckoutParams) GitCredsSecret() *corev1.SecretVolumeSource {
	if co == nil {
		return nil
	}
	return co.GitCredentialsSecret
}
