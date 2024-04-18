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
			result, err := scheduler.PatchPodSpec(c.podspec, c.patch)
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

	input := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", string(pluginsJSON))},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	wrapper := scheduler.NewJobWrapper(
		zaptest.NewLogger(t),
		input,
		scheduler.Config{AgentToken: "token-secret"},
	)
	result, err := wrapper.ParsePlugins().Build(false)
	require.NoError(t, err)

	assert.Len(t, result.Spec.Template.Spec.Containers, 3)

	commandContainer := findContainer(t, result.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	assert.Equal(t, pluginConfig.PodSpec.Containers[0].Command[0], commandEnv.Value)

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

	tagLabel := result.Labels["tag.buildkite.com/queue"]
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

	input := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", string(pluginsJSON))},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	wrapper := scheduler.NewJobWrapper(logger, input, scheduler.Config{AgentToken: "token-secret"})
	result, err := wrapper.ParsePlugins().Build(false)
	require.NoError(t, err)

	container := findContainer(t, result.Spec.Template.Spec.Containers, "agent")
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
	input := &api.CommandJob{
		Uuid:            "abc",
		Command:         "echo hello world",
		AgentQueryRules: []string{},
	}
	wrapper := scheduler.NewJobWrapper(zaptest.NewLogger(t), input, scheduler.Config{})
	result, err := wrapper.ParsePlugins().Build(false)
	require.NoError(t, err)

	require.Len(t, result.Spec.Template.Spec.Containers, 3)

	commandContainer := findContainer(t, result.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	require.Equal(t, input.Command, commandEnv.Value)
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

	wrapper := scheduler.NewJobWrapper(
		zaptest.NewLogger(t),
		&api.CommandJob{
			Uuid:            "abc",
			Command:         "echo hello world",
			Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
			AgentQueryRules: []string{"queue=kubernetes"},
		},
		scheduler.Config{
			Namespace:  "buildkite",
			Image:      "buildkite/agent:latest",
			AgentToken: "bkcq_1234567890",
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
	).ParsePlugins()

	job, err := wrapper.Build(false)
	require.NoError(t, err)

	require.Len(t, job.Spec.Template.Spec.Containers, 3)

	container0 := findContainer(t, job.Spec.Template.Spec.Containers, "container-0")
	if diff := cmp.Diff(container0.Image, "alpine:latest"); diff != "" {
		t.Errorf("unexpected container image (-want +got):\n%s", diff)
	}

	checkoutContainer := findContainer(t, job.Spec.Template.Spec.Containers, "checkout")
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

	wrapper := scheduler.NewJobWrapper(
		zaptest.NewLogger(t),
		&api.CommandJob{
			Uuid:            "abc",
			Command:         "echo hello world",
			Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
			AgentQueryRules: []string{"queue=kubernetes"},
		},
		scheduler.Config{
			Namespace:  "buildkite",
			Image:      "buildkite/agent:latest",
			AgentToken: "bkcq_1234567890",
		},
	).ParsePlugins()

	job, err := wrapper.Build(false)
	require.NoError(t, err)

	require.Len(t, job.Spec.Template.Spec.Containers, 2)

	container0 := findContainer(t, job.Spec.Template.Spec.Containers, "container-0")
	if diff := cmp.Diff(container0.Image, "buildkite/agent:latest"); diff != "" {
		t.Errorf("unexpected container image (-want +got):\n%s", diff)
	}

	for _, container := range job.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			t.Error("with `checkout: skip: true`: checkout container is present, want no checkout container")
		}
	}
}

func TestBuild_KubernetesStyleCommand(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    podSpec:
      containers:
        - name: echo
          image: 'alpine:latest'
          command:
            - echo
          args:
            - "'Hello'"
        - name: cowsay
          image: 'debian:latest'
          command:
            - cowsay
          args:
            - "'Hello'"
`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	wrapper := scheduler.NewJobWrapper(
		zaptest.NewLogger(t),
		&api.CommandJob{
			Uuid:            "abc",
			Command:         "",
			Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
			AgentQueryRules: []string{"queue=kubernetes"},
		},
		scheduler.Config{
			Namespace:  "buildkite",
			Image:      "buildkite/agent:latest",
			AgentToken: "bkcq_1234567890",
		},
	).ParsePlugins()

	job, err := wrapper.Build(false)
	require.NoError(t, err)

	require.Len(t, job.Spec.Template.Spec.Containers, 4)

	echo := findContainer(t, job.Spec.Template.Spec.Containers, "echo")
	if got, want := echo.Image, "alpine:latest"; got != want {
		t.Errorf("echo.Image = %q, want %q", got, want)
	}
	echoCmd := findEnv(t, echo.Env, "BUILDKITE_COMMAND")
	if got, want := echoCmd.Value, "echo 'Hello'"; got != want {
		t.Errorf("echo BUILDKITE_COMMAND = %q, want %q", got, want)
	}

	cowsay := findContainer(t, job.Spec.Template.Spec.Containers, "cowsay")
	if got, want := cowsay.Image, "debian:latest"; got != want {
		t.Errorf("cowsay.Image = %q, want %q", got, want)
	}
	cowsayCmd := findEnv(t, cowsay.Env, "BUILDKITE_COMMAND")
	if got, want := cowsayCmd.Value, "cowsay 'Hello'"; got != want {
		t.Errorf("cowsay BUILDKITE_COMMAND = %q, want %q", got, want)
	}
}

func TestBuild_BuildkiteStyleMultiCommand(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    multi_command: true
    podSpec:
      containers:
        - name: echo
          image: 'alpine:latest'
          command:
            - echo 'Hello'
            - echo 'Goodbye'
          args:
            - "'arg1'"
            - "'arg2'"
        - name: cowsay
          image: 'debian:latest'
          command:
            - cowsay 'Hello'
            - cowsay 'Goodbye'
          args:
            - "'arg1'"
            - "'arg2'"
`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	wrapper := scheduler.NewJobWrapper(
		zaptest.NewLogger(t),
		&api.CommandJob{
			Uuid:            "abc",
			Command:         "",
			Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
			AgentQueryRules: []string{"queue=kubernetes"},
		},
		scheduler.Config{
			Namespace:  "buildkite",
			Image:      "buildkite/agent:latest",
			AgentToken: "bkcq_1234567890",
		},
	).ParsePlugins()

	job, err := wrapper.Build(false)
	require.NoError(t, err)

	require.Len(t, job.Spec.Template.Spec.Containers, 4)

	echo := findContainer(t, job.Spec.Template.Spec.Containers, "echo")
	if got, want := echo.Image, "alpine:latest"; got != want {
		t.Errorf("echo.Image = %q, want %q", got, want)
	}
	echoCmd := findEnv(t, echo.Env, "BUILDKITE_COMMAND")
	if got, want := echoCmd.Value, "echo 'Hello'\necho 'Goodbye' 'arg1' 'arg2'"; got != want {
		t.Errorf("echo BUILDKITE_COMMAND = %q, want %q", got, want)
	}

	cowsay := findContainer(t, job.Spec.Template.Spec.Containers, "cowsay")
	if got, want := cowsay.Image, "debian:latest"; got != want {
		t.Errorf("cowsay.Image = %q, want %q", got, want)
	}
	cowsayCmd := findEnv(t, cowsay.Env, "BUILDKITE_COMMAND")
	if got, want := cowsayCmd.Value, "cowsay 'Hello'\ncowsay 'Goodbye' 'arg1' 'arg2'"; got != want {
		t.Errorf("cowsay BUILDKITE_COMMAND = %q, want %q", got, want)
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

	input := &api.CommandJob{
		Uuid:            "abc",
		Env:             []string{fmt.Sprintf("BUILDKITE_PLUGINS=%s", pluginsJSON)},
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	wrapper := scheduler.NewJobWrapper(zaptest.NewLogger(t), input, scheduler.Config{})
	_, err = wrapper.ParsePlugins().Build(false)
	require.Error(t, err)

	result, err := wrapper.BuildFailureJob(err)
	require.NoError(t, err)

	commandContainer := findContainer(t, result.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	assert.Equal(
		t,
		`echo "failed parsing Kubernetes plugin: json: cannot unmarshal string into Go value of type scheduler.KubernetesPlugin" && exit 1`,
		commandEnv.Value,
	)

	for _, c := range result.Spec.Template.Spec.Containers {
		assert.NotEqual(t, c.Name, "checkout")
	}
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
