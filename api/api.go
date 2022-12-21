package api

import (
	"strings"
	"time"
)

const (
	UUIDLabel         = "buildkite.com/job-uuid"
	TagLabel          = "buildkite.com/job-tag"
	DefaultNamespace  = "default"
	DefaultAgentImage = "ghcr.io/buildkite/agent-k8s:latest"
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

type Config struct {
	AgentTokenSecret string `mapstructure:"agent-token-secret"`
	BuildkiteToken   string `mapstructure:"buildkite-token"`
	Debug            bool
	Image            string
	JobTTL           time.Duration `mapstructure:"job-ttl"`
	MaxInFlight      int           `mapstructure:"max-in-flight"`
	Namespace        string
	Org              string
	Tags             []string
}
