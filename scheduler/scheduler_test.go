package scheduler

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
)

//go:generate mockgen -destination=mock_handler_test.go -source=scheduler.go -package scheduler_test
func TestJobPluginConversion(t *testing.T) {
	pluginConfig := KubernetesPlugin{
		PodSpec: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image:   "alpine:latest",
					Command: []string{"hello world a=b=c"},
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
		{
			"github.com/buildkite-plugins/some-other-buildkite-plugin": map[string]interface{}{"foo": "bar"},
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
	worker := worker{logger: zaptest.NewLogger(t)}
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

	pluginsEnv := findEnv(t, commandContainer.Env, "BUILDKITE_PLUGINS")
	require.Equal(t, pluginsEnv.Value, `[{"github.com/buildkite-plugins/some-other-buildkite-plugin":{"foo":"bar"}}]`)
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
	pluginsEnv := findEnv(t, commandContainer.Env, "BUILDKITE_PLUGINS")
	require.Nil(t, pluginsEnv)
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

func findEnv(t *testing.T, envs []corev1.EnvVar, name string) *corev1.EnvVar {
	for _, env := range envs {
		if env.Name == name {
			return &env
		}
	}

	return nil
}
