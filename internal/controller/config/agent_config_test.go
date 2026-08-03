package config

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func envValue(ctr *corev1.Container, name string) (string, bool) {
	for _, e := range ctr.Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestApplyToAgentStart_Tracing(t *testing.T) {
	agentConfig := &AgentConfig{
		TracingBackend:              new("opentelemetry"),
		TracingServiceName:          new("my-service"),
		TracingPropagateTraceparent: new(true),
	}

	ctr := &corev1.Container{}
	agentConfig.ApplyToAgentStart(ctr)

	want := map[string]string{
		"BUILDKITE_TRACING_BACKEND":               "opentelemetry",
		"BUILDKITE_TRACING_SERVICE_NAME":          "my-service",
		"BUILDKITE_TRACING_PROPAGATE_TRACEPARENT": "true",
	}
	for name, wantVal := range want {
		got, ok := envValue(ctr, name)
		if !ok {
			t.Errorf("env %s not set, want %q", name, wantVal)
			continue
		}
		if got != wantVal {
			t.Errorf("env %s = %q, want %q", name, got, wantVal)
		}
	}
}

func TestApplyToAgentStart_TracingUnset(t *testing.T) {
	ctr := &corev1.Container{}
	(&AgentConfig{}).ApplyToAgentStart(ctr)

	for _, name := range []string{
		"BUILDKITE_TRACING_BACKEND",
		"BUILDKITE_TRACING_SERVICE_NAME",
		"BUILDKITE_TRACING_PROPAGATE_TRACEPARENT",
	} {
		if val, ok := envValue(ctr, name); ok {
			t.Errorf("env %s unexpectedly set to %q", name, val)
		}
	}
}
