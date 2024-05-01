package config

import (
	"time"

	"github.com/buildkite/agent/v3/version"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
)

const (
	UUIDLabel                  = "buildkite.com/job-uuid"
	BuildURLAnnotation         = "buildkite.com/build-url"
	JobURLAnnotation           = "buildkite.com/job-url"
	DefaultNamespace           = "default"
	DefaultPreScheduleHookPath = "/etc/agent-stack-k8s/pre-schedule"
)

var DefaultAgentImage = "ghcr.io/buildkite/agent:" + version.Version()

// viper requires mapstructure struct tags, but the k8s types only have json struct tags.
// mapstructure (the module) supports switching the struct tag to "json", viper does not. So we have
// to have the `mapstructure` tag for viper and the `json` tag is used by the mapstructure!
type Config struct {
	Debug                  bool            `json:"debug"`
	JobTTL                 time.Duration   `json:"job-ttl"`
	AgentTokenSecret       string          `json:"agent-token-secret"       validate:"required"`
	BuildkiteToken         string          `json:"buildkite-token"          validate:"required"`
	Image                  string          `json:"image"                    validate:"required"`
	MaxInFlight            int             `json:"max-in-flight"            validate:"min=0"`
	Namespace              string          `json:"namespace"                validate:"required"`
	Org                    string          `json:"org"                      validate:"required"`
	Tags                   stringSlice     `json:"tags"                     validate:"min=1"`
	ProfilerAddress        string          `json:"profiler-address"         validate:"omitempty,hostname_port"`
	ClusterUUID            string          `json:"cluster-uuid"             validate:"omitempty"`
	AdditionalRedactedVars stringSlice     `json:"additional-redacted-vars" validate:"omitempty"`
	PodSpecPatch           *corev1.PodSpec `json:"pod-spec-patch"           validate:"omitempty"`
	PreScheduleHookPath    string          `json:"pre-schedule-hook-path"   validate:"omitempty"`
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
	enc.AddInt("max-in-flight", c.MaxInFlight)
	enc.AddString("namespace", c.Namespace)
	enc.AddString("org", c.Org)
	enc.AddString("profiler-address", c.ProfilerAddress)
	enc.AddString("cluster-uuid", c.ClusterUUID)
	enc.AddString("pre-schedule-hook-path", c.PreScheduleHookPath)
	if err := enc.AddArray("additional-redacted-vars", c.AdditionalRedactedVars); err != nil {
		return err
	}
	if err := enc.AddReflected("pod-spec-patch", c.PodSpecPatch); err != nil {
		return err
	}
	return enc.AddArray("tags", c.Tags)
}
