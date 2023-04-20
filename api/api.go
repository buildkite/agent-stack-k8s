package api

//go:generate go run github.com/Khan/genqlient

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
)

const (
	UUIDLabel          = "buildkite.com/job-uuid"
	TagLabel           = "buildkite.com/job-tag"
	BuildURLAnnotation = "buildkite.com/build-url"
	JobURLAnnotation   = "buildkite.com/job-url"
	DefaultNamespace   = "default"
	DefaultAgentImage  = "ghcr.io/buildkite/agent-k8s:latest"
)

// a valid label must be an empty string or consist of alphanumeric characters,
// '-', '_' or '.', and must start and end with an alphanumeric character (e.g.
// 'MyValue',  or 'my_value',  or '12345', regex used for validation is
// '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')
func TagToLabel(tag string) string {
	return strings.ReplaceAll(tag, "=", "_")
}

func TagsToLabels(tags []string) []string {
	labels := make([]string, len(tags))
	for i, tag := range tags {
		labels[i] = TagToLabel(tag)
	}
	return labels
}

func JobName(uuid string) string {
	return fmt.Sprintf("buildkite-%s", uuid)
}

type Config struct {
	AgentTokenSecret string `mapstructure:"agent-token-secret" validate:"required"`
	BuildkiteToken   string `mapstructure:"buildkite-token" validate:"required"`
	Debug            bool
	Image            string        `validate:"required"`
	JobTTL           time.Duration `mapstructure:"job-ttl"`
	MaxInFlight      int           `mapstructure:"max-in-flight" validate:"min=0"`
	Namespace        string        `validate:"required"`
	Org              string        `validate:"required"`
	Tags             []string      `validate:"min=1"`
	ProfilerAddress  string        `mapstructure:"profiler-address" validate:"omitempty,hostname_port"`
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
	return enc.AddReflected("tags", c.Tags)
}
