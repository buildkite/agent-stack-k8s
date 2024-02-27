package config

import (
	"time"

	"go.uber.org/zap/zapcore"
)

const (
	UUIDLabel          = "buildkite.com/job-uuid"
	BuildURLAnnotation = "buildkite.com/build-url"
	JobURLAnnotation   = "buildkite.com/job-url"
	DefaultNamespace   = "default"
	DefaultAgentImage  = "ghcr.io/buildkite/agent-stack-k8s/agent:latest"
)

type Config struct {
	Debug                bool                   `mapstructure:"debug"`
	AgentTokenSecret     string                 `mapstructure:"agent-token-secret" validate:"required"`
	BuildkiteToken       string                 `mapstructure:"buildkite-token"    validate:"required"`
	Image                string                 `mapstructure:"image"              validate:"required"`
	JobTTL               time.Duration          `mapstructure:"job-ttl"`
	MaxInFlight          int                    `mapstructure:"max-in-flight"      validate:"min=0"`
	Namespace            string                 `mapstructure:"namespace"          validate:"required"`
	Org                  string                 `mapstructure:"org"                validate:"required"`
	Tags                 stringSlice            `mapstructure:"tags"               validate:"min=1"`
	ProfilerAddress      string                 `mapstructure:"profiler-address"   validate:"omitempty,hostname_port"`
	ClusterUUID          string                 `mapstructure:"cluster-uuid"       validate:"omitempty"`
	PodSpecPatch         map[string]interface{} `mapstructure:"pod-spec-patch"     validate:"omitempty"`
	SSHCredentialsSecret string                 `mapstructure:"ssh-credentials-secret" validate:"omitempty"`
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
	enc.AddString("ssh-credentials-secret", c.SSHCredentialsSecret)
	enc.AddBool("debug", c.Debug)
	enc.AddString("image", c.Image)
	enc.AddDuration("job-ttl", c.JobTTL)
	enc.AddInt("max-in-flight", c.MaxInFlight)
	enc.AddString("namespace", c.Namespace)
	enc.AddString("org", c.Org)
	enc.AddString("profiler-address", c.ProfilerAddress)
	enc.AddString("cluster-uuid", c.ClusterUUID)
	if err := enc.AddReflected("pod-spec-patch", c.PodSpecPatch); err != nil {
		return err
	}
	return enc.AddArray("tags", c.Tags)
}
