package scheduler_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func TestPatchPodSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		podspec   *corev1.PodSpec
		patch     *corev1.PodSpec
		assertion func(t *testing.T, result *corev1.PodSpec)
		err       error
	}{
		{
			name: "patching a container",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Image: "alpine:latest",
						Command: []string{
							"echo hello world",
						},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Image: "debian:latest",
						Name:  "my-cool-container",
					},
				},
			},
			assertion: func(t *testing.T, result *corev1.PodSpec) {
				assert.Equal(t, "debian:latest", result.Containers[0].Image)
			},
		},
		{
			name: "patching container commands should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Image: "alpine:latest",
						Command: []string{
							"echo hello world",
						},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "my-cool-container",
						Command: []string{"this", "shouldn't", "work"},
					},
				},
			},
			err: scheduler.ErrNoCommandModification,
		},
		{
			name: "patching container args should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Image: "alpine:latest",
						Command: []string{
							"echo hello world",
						},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "my-cool-container",
						Args: []string{"this", "also", "shouldn't", "work"},
					},
				},
			},
			err: scheduler.ErrNoCommandModification,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := scheduler.PatchPodSpec(c.podspec, c.patch, false)
			if c.err != nil {
				require.Error(t, err)
				require.ErrorIs(t, c.err, err)
			} else {
				c.assertion(t, result)
			}
		})
	}
}

func TestJobPluginConversion(t *testing.T) {
	t.Parallel()
	pluginConfig := scheduler.KubernetesPlugin{
		PodSpec: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image:   "alpine:latest",
					Command: []string{"hello world a=b=c"},
					EnvFrom: []corev1.EnvFromSource{
						{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "some-configmap",
								},
							},
						},
					},
				},
			},
		},
		GitEnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "git-secret"},
				},
			},
		},
	}
	pluginsJSON, err := json.Marshal([]map[string]interface{}{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": pluginConfig,
		},
		{
			"github.com/buildkite-plugins/some-other-buildkite-plugin": map[string]interface{}{
				"foo": "bar",
			},
		},
	})
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", string(pluginsJSON))},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := scheduler.New(
		zaptest.NewLogger(t),
		nil,
		scheduler.Config{
			AgentTokenSecretName: "token-secret",
		},
	)
	inputs, err := worker.ParseJob(job)
	require.NoError(t, err)
	kjob, err := worker.Build(pluginConfig.PodSpec, false, inputs)
	require.NoError(t, err)

	gotPodSpec := kjob.Spec.Template.Spec

	assert.Len(t, gotPodSpec.Containers, 3)

	commandContainer := findContainer(t, gotPodSpec.Containers, "container-0")

	// Command should be replaced with tini-static.
	// Args should be set to -- buildkite-agent bootstrap.
	// The original command should be placed in BUILDKITE_COMMAND.
	wantCommand := []string{"/workspace/tini-static"}
	if diff := cmp.Diff(commandContainer.Command, wantCommand); diff != "" {
		t.Errorf("kjob.Spec.Template.Spec.Containers[0].Command diff (-got +want):\n%s", diff)
	}
	wantArgs := []string{"--", "/workspace/buildkite-agent", "bootstrap"}
	if diff := cmp.Diff(commandContainer.Args, wantArgs); diff != "" {
		t.Errorf("kjob.Spec.Template.Spec.Containers[0].Args diff (-got +want):\n%s", diff)
	}

	bkCommandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	if got, want := bkCommandEnv.Value, "hello world a=b=c"; got != want {
		t.Errorf("commandContainer.Env[BUILDKITE_COMMAND].Value = %q, want %q", got, want)
	}

	var envFromNames []string
	for _, envFrom := range commandContainer.EnvFrom {
		if envFrom.ConfigMapRef != nil {
			envFromNames = append(envFromNames, envFrom.ConfigMapRef.Name)
		}
		if envFrom.SecretRef != nil {
			envFromNames = append(envFromNames, envFrom.SecretRef.Name)
		}
	}
	require.ElementsMatch(t, envFromNames, []string{"some-configmap", "git-secret"})

	tokenEnv := findEnv(t, commandContainer.Env, "BUILDKITE_AGENT_TOKEN")
	assert.Equal(t, "token-secret", tokenEnv.ValueFrom.SecretKeyRef.Name)

	tagLabel := kjob.Labels["tag.buildkite.com/queue"]
	assert.Equal(t, tagLabel, "kubernetes")

	pluginsEnv := findEnv(t, commandContainer.Env, "BUILDKITE_PLUGINS")
	assert.Equal(
		t, pluginsEnv.Value, `[{"github.com/buildkite-plugins/some-other-buildkite-plugin":{"foo":"bar"}}]`,
	)
}

func TestTagEnv(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	pluginConfig := scheduler.KubernetesPlugin{
		PodSpec: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image:   "alpine:latest",
					Command: []string{"hello world a=b=c"},
				},
			},
		},
	}
	pluginsJSON, err := json.Marshal([]map[string]any{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": pluginConfig,
		},
	})
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", string(pluginsJSON))},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := scheduler.New(
		logger,
		nil,
		scheduler.Config{
			AgentTokenSecretName: "token-secret",
		},
	)
	inputs, err := worker.ParseJob(job)
	require.NoError(t, err)
	kjob, err := worker.Build(pluginConfig.PodSpec, false, inputs)
	require.NoError(t, err)

	container := findContainer(t, kjob.Spec.Template.Spec.Containers, "agent")
	assertEnvFieldPath(t, container, "BUILDKITE_K8S_NODE", "spec.nodeName")
	assertEnvFieldPath(t, container, "BUILDKITE_K8S_NAMESPACE", "metadata.namespace")
	assertEnvFieldPath(t, container, "BUILDKITE_K8S_SERVICE_ACCOUNT", "spec.serviceAccountName")
}

func assertEnvFieldPath(t *testing.T, container corev1.Container, envVarName, fieldPath string) {
	t.Helper()

	env := findEnv(t, container.Env, envVarName)
	if assert.NotNil(t, env) {
		assert.Equal(t, env.Value, "")
		hasFieldRef := assert.NotNil(t, env.ValueFrom) && assert.NotNil(t, env.ValueFrom.FieldRef)
		if hasFieldRef {
			assert.Equal(t, env.ValueFrom.FieldRef.FieldPath, fieldPath)
		}
	}
}

func TestJobWithNoKubernetesPlugin(t *testing.T) {
	t.Parallel()
	job := &api.CommandJob{
		Uuid:            "abc",
		Command:         "echo hello world",
		AgentQueryRules: []string{},
	}
	worker := scheduler.New(zaptest.NewLogger(t), nil, scheduler.Config{})
	inputs, err := worker.ParseJob(job)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	require.Len(t, kjob.Spec.Template.Spec.Containers, 3)

	commandContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	require.Equal(t, job.Command, commandEnv.Value)
	pluginsEnv := findEnv(t, commandContainer.Env, "BUILDKITE_PLUGINS")
	require.Nil(t, pluginsEnv)
}

func TestBuild(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    podSpecPatch:
      containers:
      - name: container-0
        image: alpine:latest`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Command:         "echo hello world",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := scheduler.New(
		zaptest.NewLogger(t),
		nil,
		scheduler.Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
			PodSpecPatch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "checkout",
						EnvFrom: []corev1.EnvFromSource{
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "git-ssh-key",
									},
								},
							},
						},
					},
				},
			},
		},
	)
	inputs, err := worker.ParseJob(job)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	require.Len(t, kjob.Spec.Template.Spec.Containers, 3)

	container0 := findContainer(t, kjob.Spec.Template.Spec.Containers, "container-0")
	if diff := cmp.Diff(container0.Image, "alpine:latest"); diff != "" {
		t.Errorf("unexpected container image (-want +got):\n%s", diff)
	}

	checkoutContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, "checkout")
	if diff := cmp.Diff(checkoutContainer.EnvFrom, []corev1.EnvFromSource{
		{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "git-ssh-key",
				},
			},
		},
	}); diff != "" {
		t.Errorf("unexpected pod spec (-want +got):\n%s", diff)
	}
}

func TestBuildSkipCheckout(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    checkout:
      skip: true`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Command:         "echo hello world",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := scheduler.New(
		zaptest.NewLogger(t),
		nil,
		scheduler.Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
		},
	)
	inputs, err := worker.ParseJob(job)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	require.Len(t, kjob.Spec.Template.Spec.Containers, 2)

	container0 := findContainer(t, kjob.Spec.Template.Spec.Containers, "container-0")
	if diff := cmp.Diff(container0.Image, "buildkite/agent:latest"); diff != "" {
		t.Errorf("unexpected container image (-want +got):\n%s", diff)
	}

	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			t.Error("with `checkout: skip: true`: checkout container is present, want no checkout container")
		}
	}
}

func TestBuildCheckoutEmptyConfigEnv(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    checkout: {}
  `

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Command:         "echo hello world",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := scheduler.New(
		zaptest.NewLogger(t),
		nil,
		scheduler.Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
		},
	)
	inputs, err := worker.ParseJob(job)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			for _, envVar := range container.Env {
				if envVar.Name == "BUILDKITE_GIT_SUBMODULE_CLONE_CONFIG" {
					t.Error("with `checkout: {}`, want no BUILDKITE_GIT_SUBMODULE_CLONE_CONFIG env on checkout container")
				}
			}
		}
	}
}

func TestFailureJobs(t *testing.T) {
	t.Parallel()
	pluginsJSON, err := json.Marshal([]map[string]any{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": `"some-invalid-json"`,
		},
	})
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	wrapper := scheduler.New(zaptest.NewLogger(t), nil, scheduler.Config{})
	_, err = wrapper.ParseJob(job)
	require.Error(t, err)
}

func TestProhibitKubernetesPlugin(t *testing.T) {
	t.Parallel()
	pluginsJSON, err := json.Marshal([]map[string]any{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": scheduler.KubernetesPlugin{},
		},
	})
	require.NoError(t, err)

	job := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := scheduler.New(zaptest.NewLogger(t), nil, scheduler.Config{
		ProhibitK8sPlugin: true,
	})
	_, err = worker.ParseJob(job)
	require.Error(t, err)
}

func findContainer(t *testing.T, containers []corev1.Container, name string) corev1.Container {
	t.Helper()

	for _, container := range containers {
		if container.Name == name {
			return container
		}
	}
	require.FailNow(t, "container not found")

	return corev1.Container{}
}

func findEnv(t *testing.T, envs []corev1.EnvVar, name string) *corev1.EnvVar {
	t.Helper()

	for _, env := range envs {
		if env.Name == name {
			return &env
		}
	}

	return nil
}
