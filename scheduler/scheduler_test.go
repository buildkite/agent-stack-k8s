package scheduler

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestJobPluginConversion(t *testing.T) {
	pluginConfig := PluginConfig{
		PodSpec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image:   "alpine:latest",
					Command: []string{"hello world"},
				},
			},
		},
		GitEnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "some-secret"},
				},
			},
		},
	}
	pluginsJSON, err := json.Marshal([]map[string]interface{}{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": pluginConfig,
		},
	})
	require.NoError(t, err)

	input := &monitor.Job{
		CommandJob: api.CommandJob{
			Uuid: "abc",
			Env:  []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", string(pluginsJSON))},
		},
		Tag: "queue=kubernetes",
	}
	worker := worker{}
	result, err := worker.k8sify(input, "some-secret")
	require.NoError(t, err)

	require.Len(t, result.Spec.Template.Spec.Containers, 3)

	commandContainer := findContainer(t, result.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	require.Equal(t, pluginConfig.PodSpec.Containers[0].Command[0], commandEnv.Value)

	tokenEnv := findEnv(t, commandContainer.Env, "BUILDKITE_AGENT_TOKEN")
	require.Equal(t, "some-secret", tokenEnv.ValueFrom.SecretKeyRef.Name)

	tagLabel := result.Labels[api.TagLabel]
	require.Equal(t, api.TagToLabel(input.Tag), tagLabel)
}

func TestJobWithNoKubernetesPlugin(t *testing.T) {
	input := &monitor.Job{
		CommandJob: api.CommandJob{
			Uuid:    "abc",
			Command: "echo hello world",
		},
	}
	worker := worker{}
	result, err := worker.k8sify(input, "secret")
	require.NoError(t, err)

	require.Len(t, result.Spec.Template.Spec.Containers, 3)

	commandContainer := findContainer(t, result.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	require.Equal(t, input.Command, commandEnv.Value)
}

func findContainer(t *testing.T, containers []corev1.Container, name string) corev1.Container {
	for _, container := range containers {
		if container.Name == name {
			return container
		}
	}
	t.Helper()
	require.FailNow(t, "container not found")

	return corev1.Container{}
}

func findEnv(t *testing.T, envs []corev1.EnvVar, name string) corev1.EnvVar {
	for _, env := range envs {
		if env.Name == name {
			return env
		}
	}
	t.Helper()
	require.FailNow(t, "envvar not found")

	return corev1.EnvVar{}
}
