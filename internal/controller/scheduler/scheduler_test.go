package scheduler

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

func TestPatchPodSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		podspec *corev1.PodSpec
		patch   *corev1.PodSpec
		want    *corev1.PodSpec
	}{
		{
			name: "patching in a new unmanaged container",
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
			want: &corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "my-cool-container",
					Image: "debian:latest",
				}, {
					Image: "alpine:latest",
					Command: []string{
						"echo hello world",
					},
				}},
			},
		},
		{
			name: "patching a sidecar container",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "sidecar-0",
						Image:   "alpine:latest",
						Command: []string{"echo hello world"},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "sidecar-0",
						Command: []string{"echo goodbye world"},
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "sidecar-0",
					Image:   "alpine:latest",
					Command: []string{"echo goodbye world"},
				}},
			},
		},
		{
			name: "patching command container commands and args should work",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "my-cool-container",
						Image:   "alpine:latest",
						Command: commandContainerCommand,
						Args:    commandContainerArgs,
						Env: []corev1.EnvVar{{
							Name: "BUILDKITE_COMMAND", Value: "echo hello world",
						}},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "my-cool-container",
						Command: []string{"this should"},
						Args:    []string{"work", "as", "expected"},
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "my-cool-container",
						Image:   "alpine:latest",
						Command: commandContainerCommand,
						Args:    commandContainerArgs,
						Env: []corev1.EnvVar{{
							Name: "BUILDKITE_COMMAND", Value: "this should work as expected",
						}},
					},
				},
			},
		},
		{
			name: "patching without overriding containers should preserve default container",
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
				HostAliases: []corev1.HostAlias{
					{
						IP:        "127.0.0.1",
						Hostnames: []string{"agent.buildkite.localhost"},
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Image: "alpine:latest",
						Command: []string{
							"echo hello world",
						},
					},
				},
				HostAliases: []corev1.HostAlias{
					{
						IP:        "127.0.0.1",
						Hostnames: []string{"agent.buildkite.localhost"},
					},
				},
			},
		},
		{
			name: "can patch image",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "container-0",
						Image: "alpine:a",
						Command: []string{
							"echo hello world",
						},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "container-0",
						Image: "alpine:b",
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "container-0",
						Image: "alpine:b",
						Command: []string{
							"echo hello world",
						},
					},
				},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := PatchPodSpec(test.podspec, test.patch, nil, nil, false)
			if err != nil {
				t.Fatalf("PodSpecPatch error = %v", err)
			}
			if diff := cmp.Diff(got, test.want); diff != "" {
				t.Errorf("PodSpecPatch result diff (-got +want):\n%s", diff)
			}
		})
	}
}

func TestPatchPodSpec_ErrNoCommandModification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		podspec *corev1.PodSpec
		patch   *corev1.PodSpec
	}{
		{
			name: "patching agent command should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    AgentContainerName,
						Image:   "alpine:latest",
						Command: []string{"echo hello world"},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    AgentContainerName,
						Command: []string{"this shouldn't work"},
					},
				},
			},
		},
		{
			name: "patching agent args should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    AgentContainerName,
						Image:   "alpine:latest",
						Command: []string{"echo hello world"},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: AgentContainerName,
						Args: []string{"this", "shouldn't", "work"},
					},
				},
			},
		},
		{
			name: "patching checkout command should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    CheckoutContainerName,
						Image:   "alpine:latest",
						Command: []string{"echo hello world"},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    CheckoutContainerName,
						Command: []string{"this shouldn't work"},
					},
				},
			},
		},
		{
			name: "patching checkout args should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    CheckoutContainerName,
						Image:   "alpine:latest",
						Command: []string{"echo hello world"},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: CheckoutContainerName,
						Args: []string{"this", "shouldn't", "work"},
					},
				},
			},
		},
		{
			name: "uppercase image name should fail",
			podspec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    CheckoutContainerName,
						Image:   "ALPINE:latest",
						Command: []string{"echo hello world"},
					},
				},
			},
			patch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: CheckoutContainerName,
						Args: []string{"this", "shouldn't", "work"},
					},
				},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := PatchPodSpec(test.podspec, test.patch, nil, nil, false)
			if !errors.Is(err, ErrNoCommandModification) {
				t.Errorf("PodSpecPatch error = %v, want ErrNoCommandModification (%v)", err, ErrNoCommandModification)
			}
		})
	}
}

func TestJobPluginConversion(t *testing.T) {
	t.Parallel()
	pluginConfig := KubernetesPlugin{
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
	pluginsJSON, err := json.Marshal([]map[string]any{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": pluginConfig,
		},
		{
			"github.com/buildkite-plugins/some-other-buildkite-plugin": map[string]any{
				"foo": "bar",
			},
		},
	})
	require.NoError(t, err)

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(
		zaptest.NewLogger(t),
		nil,
		nil,
		Config{
			AgentTokenSecretName: "token-secret",
			Image:                "buildkite/agent:latest",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	require.NoError(t, err)
	kjob, err := worker.Build(pluginConfig.PodSpec, false, inputs)
	require.NoError(t, err)

	gotPodSpec := kjob.Spec.Template.Spec

	assert.Len(t, gotPodSpec.Containers, 3)

	agentContainer := findContainer(t, gotPodSpec.Containers, "agent")

	tokenEnv := findEnv(t, agentContainer.Env, "BUILDKITE_AGENT_TOKEN")
	assert.Equal(t, "token-secret", tokenEnv.ValueFrom.SecretKeyRef.Name)

	commandContainer := findContainer(t, gotPodSpec.Containers, "container-0")

	// Command should be replaced with tini-static.
	// Args should be set to -- buildkite-agent kubernetes-bootstrap.
	// The original command should be placed in BUILDKITE_COMMAND.
	wantCommand := []string{"/workspace/tini-static"}
	if diff := cmp.Diff(commandContainer.Command, wantCommand); diff != "" {
		t.Errorf("kjob.Spec.Template.Spec.Containers[0].Command diff (-got +want):\n%s", diff)
	}
	wantArgs := []string{"--", "/workspace/buildkite-agent", "kubernetes-bootstrap"}
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

	tagLabel := kjob.Labels["tag.buildkite.com/queue"]
	assert.Equal(t, tagLabel, "kubernetes")
}

func TestTagEnv(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	pluginConfig := KubernetesPlugin{
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

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(
		logger,
		nil,
		nil,
		Config{
			AgentTokenSecretName: "token-secret",
			Image:                "buildkite/agent:latest",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
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
	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(zaptest.NewLogger(t), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
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

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		zaptest.NewLogger(t),
		nil,
		nil,
		Config{
			ID:                   "controller-1",
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
	inputs, err := worker.ParseJob(job, sjob)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	require.Len(t, kjob.Spec.Template.Spec.Containers, 3)

	controllerIDLabel := kjob.Labels["buildkite.com/controller-id"]
	assert.Equal(t, controllerIDLabel, "controller-1")

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

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		zaptest.NewLogger(t),
		nil,
		nil,
		Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
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

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		zaptest.NewLogger(t),
		nil,
		nil,
		Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
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

// DefaultCheckoutParams comes from helm values
func TestBuildDefaultCheckoutParams(t *testing.T) {
	t.Parallel()
	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(zaptest.NewLogger(t), nil, nil, Config{
		Image: "buildkite/agent:latest",
		DefaultCheckoutParams: &config.CheckoutParams{
			GitCredentialsSecret: &corev1.SecretVolumeSource{
				SecretName: "bluh",
			},
			EnvFrom: []corev1.EnvFromSource{
				{
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "some-secret-env",
						},
					},
				},
			},
			ExtraVolumeMounts: []corev1.VolumeMount{
				{
					Name: "extra-volume-something",
				},
			},
		},
	})
	inputs, err := worker.ParseJob(job, sjob)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	var checkoutContainer *corev1.Container
	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			checkoutContainer = &container
		}
	}

	require.NotNil(t, checkoutContainer)

	// Validate that git credential secret is mounted and available in checkout container's path
	var hasGitCredentialsRO, hasGitCredentials bool
	for _, mount := range checkoutContainer.VolumeMounts {
		if mount.Name == "git-credentials-ro" && mount.MountPath == "/buildkite/git-credentials-ro" {
			hasGitCredentialsRO = true
		}
		if mount.Name == "git-credentials" && mount.MountPath == "/buildkite/git-credentials" {
			hasGitCredentials = true
		}
	}

	if !hasGitCredentialsRO {
		t.Error("checkout container missing git-credentials-ro volume mount at /buildkite/git-credentials-ro")
	}
	if !hasGitCredentials {
		t.Error("checkout container missing git-credentials volume mount at /buildkite/git-credentials")
	}

	// Validate that the EnvFrom is passed down to checkout container pod spec
	var hasSecretEnvFrom bool
	for _, envFrom := range checkoutContainer.EnvFrom {
		if envFrom.SecretRef != nil && envFrom.SecretRef.Name == "some-secret-env" {
			hasSecretEnvFrom = true
			break
		}
	}
	if !hasSecretEnvFrom {
		t.Error("checkout container missing EnvFrom with secret 'some-secret-env'")
	}

	// Validate that ExtraVolumeMounts is passed down to the checkout container pod spec
	var hasExtraVolumeMount bool
	for _, mount := range checkoutContainer.VolumeMounts {
		if mount.Name == "extra-volume-something" {
			hasExtraVolumeMount = true
			break
		}
	}
	if !hasExtraVolumeMount {
		t.Error("checkout container missing ExtraVolumeMount 'extra-volume-something'")
	}
}

// CheckoutParams come from our bk yaml
func TestBuildCheckoutParams(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    checkout:
      gitCredentialsSecret:
        secretName: "bluh"
      envFrom:
        - secretRef:
            name: "some-secret-env"
      extraVolumeMounts:
        - name: "extra-volume-something"
  `

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		zaptest.NewLogger(t),
		nil,
		nil,
		Config{
			Namespace: "buildkite",
			Image:     "buildkite/agent:latest",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	var checkoutContainer *corev1.Container
	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			checkoutContainer = &container
		}
	}

	require.NotNil(t, checkoutContainer)

	// Validate that git credential secret is mounted and available in checkout container's path
	var hasGitCredentialsRO, hasGitCredentials bool
	for _, mount := range checkoutContainer.VolumeMounts {
		if mount.Name == "git-credentials-ro" && mount.MountPath == "/buildkite/git-credentials-ro" {
			hasGitCredentialsRO = true
		}
		if mount.Name == "git-credentials" && mount.MountPath == "/buildkite/git-credentials" {
			hasGitCredentials = true
		}
	}

	if !hasGitCredentialsRO {
		t.Error("checkout container missing git-credentials-ro volume mount at /buildkite/git-credentials-ro")
	}
	if !hasGitCredentials {
		t.Error("checkout container missing git-credentials volume mount at /buildkite/git-credentials")
	}

	// Validate that the EnvFrom is passed down to checkout container pod spec
	var hasSecretEnvFrom bool
	for _, envFrom := range checkoutContainer.EnvFrom {
		if envFrom.SecretRef != nil && envFrom.SecretRef.Name == "some-secret-env" {
			hasSecretEnvFrom = true
			break
		}
	}
	if !hasSecretEnvFrom {
		t.Error("checkout container missing EnvFrom with secret 'some-secret-env'")
	}

	// Validate that ExtraVolumeMounts is passed down to the checkout container pod spec
	var hasExtraVolumeMount bool
	for _, mount := range checkoutContainer.VolumeMounts {
		if mount.Name == "extra-volume-something" {
			hasExtraVolumeMount = true
			break
		}
	}
	if !hasExtraVolumeMount {
		t.Error("checkout container missing ExtraVolumeMount 'extra-volume-something'")
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

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	wrapper := New(zaptest.NewLogger(t), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	_, err = wrapper.ParseJob(job, sjob)
	require.Error(t, err)
}

func TestProhibitKubernetesPlugin(t *testing.T) {
	t.Parallel()
	pluginsJSON, err := json.Marshal([]map[string]any{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": KubernetesPlugin{},
		},
	})
	require.NoError(t, err)

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(zaptest.NewLogger(t), nil, nil, Config{
		Image:             "buildkite/agent:latest",
		ProhibitK8sPlugin: true,
	})
	_, err = worker.ParseJob(job, sjob)
	require.Error(t, err)
}

func TestCustomImageSyntax_pluginTakesTopPriority(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    podSpecPatch:
      containers:
      - name: container-0
        image: "x-image:plugin"`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	require.NoError(t, err)

	job := &api.AgentJob{
		ID: "abc",
		Env: map[string]string{
			"BUILDKITE_PLUGINS": string(pluginsJSON),
			"BUILDKITE_IMAGE":   "x-image:job",
		},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(zaptest.NewLogger(t), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	commandContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, CommandCommanderName)
	require.Equal(t, "x-image:plugin", commandContainer.Image)
}

// Job level image syntax takes priority over controller setting
func TestCustomImageSyntax_jobLevelImagePriority(t *testing.T) {
	t.Parallel()

	job := &api.AgentJob{
		ID: "abc",
		Env: map[string]string{
			"BUILDKITE_IMAGE": "x-image:job",
		},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(zaptest.NewLogger(t), nil, nil, Config{
		Image: "buildkite/agent:latest",
		PodSpecPatch: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "container-0",
					Image: "alpine:controller",
				},
			},
		},
	})
	inputs, err := worker.ParseJob(job, sjob)
	require.NoError(t, err)
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	require.NoError(t, err)

	commandContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, CommandCommanderName)
	require.Equal(t, "x-image:job", commandContainer.Image)
}

func TestImagePullPolicies(t *testing.T) {
	t.Parallel()

	const (
		defaultImage       = "buildkite/agent:3.92.1"
		imageWithoutDigest = "golang:1.23.5"
		imageWithDigest    = "golang:1.23.5@sha256:8c10f21bec412f08f73aa7b97ca5ac5f28a39d8a88030ad8a339fd0a781d72b4"
	)

	tests := []struct {
		name                      string
		cfgDefaultCheckPullPolicy corev1.PullPolicy
		cfgDefaultPullPolicy      corev1.PullPolicy
		podSpecContainers         []corev1.Container
		wantImageChecks           map[string]corev1.PullPolicy
		wantContainers            map[string]corev1.PullPolicy
	}{
		// --------- The defaults ----------
		{
			name:            "empty defaults, empty podspec",
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled always by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name: "empty defaults, image without digest",
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullAlways,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name: "empty defaults, image with digest",
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithDigest: corev1.PullIfNotPresent,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullAlways,
			},
		},

		// --------- Pulling images always ----------
		{
			name: "empty defaults, container pull Always",
			podSpecContainers: []corev1.Container{{
				Name:            "container-0",
				Image:           imageWithoutDigest,
				ImagePullPolicy: corev1.PullAlways,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullAlways,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"copy-agent":  corev1.PullAlways,
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
			},
		},
		{
			name:                 "default pull Always, empty podspec",
			cfgDefaultPullPolicy: corev1.PullAlways,
			wantImageChecks:      map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                      "default check pull Always, empty podspec",
			cfgDefaultCheckPullPolicy: corev1.PullAlways,
			wantImageChecks:           map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull Always, image without digest",
			cfgDefaultPullPolicy: corev1.PullAlways,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullAlways,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                      "default check pull Always, image without digest",
			cfgDefaultCheckPullPolicy: corev1.PullAlways,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullAlways,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull Always, image with digest",
			cfgDefaultPullPolicy: corev1.PullAlways,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithDigest: corev1.PullAlways,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                      "default check pull Always, image with digest",
			cfgDefaultCheckPullPolicy: corev1.PullAlways,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithDigest: corev1.PullAlways,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent, // because it was just pulled
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,
			},
		},

		// --------- Pulling IfNotPresent ----------
		{
			name: "empty defaults, container pull IfNotPresent",
			podSpecContainers: []corev1.Container{{
				Name:            "container-0",
				Image:           imageWithoutDigest,
				ImagePullPolicy: corev1.PullIfNotPresent,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullIfNotPresent,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull IfNotPresent, empty podspec",
			cfgDefaultPullPolicy: corev1.PullIfNotPresent,
			wantImageChecks:      map[string]corev1.PullPolicy{
				// default image pulled IfNotPresent by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullIfNotPresent,
			},
		},
		{
			name:                      "default check pull IfNotPresent, empty podspec",
			cfgDefaultCheckPullPolicy: corev1.PullIfNotPresent,
			wantImageChecks:           map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullAlways, // TODO: does this make sense?
				"agent":       corev1.PullAlways,
				"checkout":    corev1.PullAlways,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull IfNotPresent, image without digest",
			cfgDefaultPullPolicy: corev1.PullIfNotPresent,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled IfNotPresent by copy-agent
				imageWithoutDigest: corev1.PullIfNotPresent,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullIfNotPresent,
			},
		},
		{
			name:                      "default check pull IfNotPresent, image without digest",
			cfgDefaultCheckPullPolicy: corev1.PullIfNotPresent,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled IfNotPresent by copy-agent
				imageWithoutDigest: corev1.PullIfNotPresent,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullAlways, // TODO: does this make sense?
				"agent":       corev1.PullAlways,
				"checkout":    corev1.PullAlways,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull IfNotPresent, image with digest",
			cfgDefaultPullPolicy: corev1.PullIfNotPresent,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled IfNotPresent by copy-agent
				imageWithDigest: corev1.PullIfNotPresent,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullIfNotPresent,
			},
		},
		{
			name:                      "default check pull IfNotPresent, image with digest",
			cfgDefaultCheckPullPolicy: corev1.PullIfNotPresent,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled IfNotPresent by copy-agent
				imageWithDigest: corev1.PullIfNotPresent,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullAlways,
				"checkout":    corev1.PullAlways,
				"copy-agent":  corev1.PullAlways,
			},
		},

		// --------- Pulling never ----------
		{
			name: "empty defaults, container pull Never",
			podSpecContainers: []corev1.Container{{
				Name:            "container-0",
				Image:           imageWithoutDigest,
				ImagePullPolicy: corev1.PullNever,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullNever,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullNever,
				"agent":       corev1.PullIfNotPresent, // because it was just pulled
				"checkout":    corev1.PullIfNotPresent, // because it was just pulled
				"copy-agent":  corev1.PullAlways,       // empty defaults
			},
		},
		{
			name:                 "default pull Never, empty podspec",
			cfgDefaultPullPolicy: corev1.PullNever,
			wantImageChecks:      map[string]corev1.PullPolicy{
				// default image pulled Never by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullNever,
				"agent":       corev1.PullNever,
				"checkout":    corev1.PullNever,
				"copy-agent":  corev1.PullNever,
			},
		},
		{
			name:                      "default check pull Never, empty podspec",
			cfgDefaultCheckPullPolicy: corev1.PullNever,
			wantImageChecks:           map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullAlways, // TODO: does this make sense?
				"agent":       corev1.PullAlways,
				"checkout":    corev1.PullAlways,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull Never, image without digest",
			cfgDefaultPullPolicy: corev1.PullNever,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Never by copy-agent
				imageWithoutDigest: corev1.PullNever,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullNever,
				"agent":       corev1.PullNever,
				"checkout":    corev1.PullNever,
				"copy-agent":  corev1.PullNever,
			},
		},
		{
			name:                      "default check pull Never, image without digest",
			cfgDefaultCheckPullPolicy: corev1.PullNever,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithoutDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithoutDigest: corev1.PullNever,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullAlways, // TODO: does this make sense?
				"agent":       corev1.PullAlways,
				"checkout":    corev1.PullAlways,
				"copy-agent":  corev1.PullAlways,
			},
		},
		{
			name:                 "default pull Never, image with digest",
			cfgDefaultPullPolicy: corev1.PullNever,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Never by copy-agent
				imageWithDigest: corev1.PullNever,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullNever,
				"agent":       corev1.PullNever,
				"checkout":    corev1.PullNever,
				"copy-agent":  corev1.PullNever,
			},
		},
		{
			name:                      "default check pull Never, image with digest",
			cfgDefaultCheckPullPolicy: corev1.PullNever,
			podSpecContainers: []corev1.Container{{
				Name:  "container-0",
				Image: imageWithDigest,
			}},
			wantImageChecks: map[string]corev1.PullPolicy{
				// default image pulled Always by copy-agent
				imageWithDigest: corev1.PullNever,
			},
			wantContainers: map[string]corev1.PullPolicy{
				"container-0": corev1.PullIfNotPresent,
				"agent":       corev1.PullAlways,
				"checkout":    corev1.PullAlways,
				"copy-agent":  corev1.PullAlways,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			worker := New(
				zaptest.NewLogger(t),
				nil,
				nil,
				Config{
					Namespace:                   "buildkite",
					Image:                       defaultImage,
					AgentTokenSecretName:        "bkcq_1234567890",
					DefaultImagePullPolicy:      test.cfgDefaultPullPolicy,
					DefaultImageCheckPullPolicy: test.cfgDefaultCheckPullPolicy,
				},
			)
			kjob, err := worker.Build(
				&corev1.PodSpec{Containers: test.podSpecContainers},
				false,
				buildInputs{
					uuid:            "1234",
					command:         "echo shell",
					agentQueryRules: []string{"queue=bernetes"},
				},
			)
			if err != nil {
				t.Fatalf("worker.Build() error = %v", err)
			}

			gotImageChecks := make(map[string]corev1.PullPolicy)
			for _, c := range kjob.Spec.Template.Spec.InitContainers {
				if !strings.HasPrefix(c.Name, ImageCheckContainerNamePrefix) {
					continue
				}
				if _, dupe := gotImageChecks[c.Image]; dupe {
					t.Errorf("duplicate image check container for image %q", c.Image)
				}
				gotImageChecks[c.Image] = c.ImagePullPolicy
			}
			if diff := cmp.Diff(gotImageChecks, test.wantImageChecks); diff != "" {
				t.Errorf("image check containers diff (-got +want):\n%s", diff)
			}

			gotContainers := make(map[string]corev1.PullPolicy)
			containers := append(kjob.Spec.Template.Spec.InitContainers, kjob.Spec.Template.Spec.Containers...)
			for _, c := range containers {
				if strings.HasPrefix(c.Name, ImageCheckContainerNamePrefix) {
					continue
				}
				if _, dupe := gotContainers[c.Name]; dupe {
					t.Errorf("duplicate container name %q", c.Image)
					continue
				}
				gotContainers[c.Name] = c.ImagePullPolicy
			}

			if diff := cmp.Diff(gotContainers, test.wantContainers); diff != "" {
				t.Errorf("other containers diff (-got +want):\n%s", diff)
			}
		})
	}
}

func TestPipelineSigningOptions(t *testing.T) {
	verificationVol := &corev1.Volume{Name: "verification-key-volume"}
	signingVol := &corev1.Volume{Name: "signing-key-volume"}

	tests := []struct {
		name              string
		agentConfig       *config.AgentConfig
		wantPodVolumes    []string
		wantNotPodVolumes []string
		wantAgentEnv      map[string]string
		wantNotAgentEnv   []string
		wantAgentMounts   []string
		wantCommandMounts []string
	}{
		{
			name:              "nil config",
			agentConfig:       nil,
			wantNotPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantNotAgentEnv: []string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE",
			},
		},
		{
			name:              "no keys",
			agentConfig:       &config.AgentConfig{},
			wantNotPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantNotAgentEnv: []string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE",
			},
		},
		{
			name: "verification key only",
			agentConfig: &config.AgentConfig{
				VerificationJWKSFile:   ptr.To("/path/to/verification.jwks"),
				VerificationJWKSVolume: verificationVol,
			},
			wantPodVolumes:    []string{verificationVol.Name},
			wantNotPodVolumes: []string{signingVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE": "/path/to/verification.jwks",
			},
			wantNotAgentEnv: []string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE",
			},
			wantAgentMounts: []string{verificationVol.Name},
		},
		{
			name: "signing key only",
			agentConfig: &config.AgentConfig{
				SigningJWKSFile:   ptr.To("/path/to/signing.jwks"),
				SigningJWKSVolume: signingVol,
			},
			wantPodVolumes:    []string{signingVol.Name},
			wantNotPodVolumes: []string{verificationVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE": "/path/to/signing.jwks",
			},
			wantNotAgentEnv: []string{
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE",
			},
			wantCommandMounts: []string{signingVol.Name},
		},
		{
			name: "both keys",
			agentConfig: &config.AgentConfig{
				VerificationJWKSFile:   ptr.To("/path/to/verification.jwks"),
				VerificationJWKSVolume: verificationVol,
				SigningJWKSFile:        ptr.To("/path/to/signing.jwks"),
				SigningJWKSVolume:      signingVol,
			},
			wantPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE":      "/path/to/signing.jwks",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE": "/path/to/verification.jwks",
			},
			wantAgentMounts:   []string{verificationVol.Name},
			wantCommandMounts: []string{signingVol.Name},
		},
		{
			name: "both volumes only",
			agentConfig: &config.AgentConfig{
				VerificationJWKSVolume: verificationVol,
				SigningJWKSVolume:      signingVol,
			},
			wantPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE":      "/buildkite/signing-jwks/key",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE": "/buildkite/verification-jwks/key",
			},
			wantAgentMounts:   []string{verificationVol.Name},
			wantCommandMounts: []string{signingVol.Name},
		},
		{
			name: "relative paths",
			agentConfig: &config.AgentConfig{
				VerificationJWKSVolume: verificationVol,
				VerificationJWKSFile:   ptr.To("my-awesome-key"),
				SigningJWKSVolume:      signingVol,
				SigningJWKSFile:        ptr.To("my-special-key"),
			},
			wantPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE":      "/buildkite/signing-jwks/my-special-key",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE": "/buildkite/verification-jwks/my-awesome-key",
			},
			wantAgentMounts:   []string{verificationVol.Name},
			wantCommandMounts: []string{signingVol.Name},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			worker := New(
				zaptest.NewLogger(t),
				nil,
				nil,
				Config{
					Namespace:            "buildkite",
					AgentTokenSecretName: "bkcq_1234567890",
					Image:                "buildkite/agent:latest",
					AgentConfig:          test.agentConfig,
				},
			)
			kjob, err := worker.Build(
				&corev1.PodSpec{},
				false,
				buildInputs{
					uuid:            "1234",
					command:         "echo shell",
					agentQueryRules: []string{"queue=bernetes"},
				},
			)
			if err != nil {
				t.Fatalf("worker.Build() error = %v", err)
			}

			// Check volumes on the pod
			podSpec := kjob.Spec.Template.Spec
			for _, wantName := range test.wantPodVolumes {
				volNameEql := func(v corev1.Volume) bool {
					return v.Name == wantName
				}
				if !slices.ContainsFunc(podSpec.Volumes, volNameEql) {
					t.Errorf("podSpec.Volumes = %v, is missing volume named %q", podSpec.Volumes, wantName)
				}
			}
			for _, wantNotName := range test.wantNotPodVolumes {
				volNameEql := func(v corev1.Volume) bool {
					return v.Name == wantNotName
				}
				if slices.ContainsFunc(podSpec.Volumes, volNameEql) {
					t.Errorf("podSpec.Volumes = %v, has unwanted volume named %q", podSpec.Volumes, wantNotName)
				}
			}

			// Check agent container env vars
			agent := findContainer(t, kjob.Spec.Template.Spec.Containers, "agent")
			for wantName, wantVal := range test.wantAgentEnv {
				env := findEnv(t, agent.Env, wantName)
				if env == nil {
					t.Errorf("agent.Env = %v, missing env var %q", agent.Env, wantName)
					continue
				}
				if env.Value != wantVal {
					t.Errorf("agent.Env[%q] = %q, want %q", wantName, env.Value, wantVal)

				}
			}
			for _, wantNotName := range test.wantNotAgentEnv {
				env := findEnv(t, agent.Env, wantNotName)
				if env != nil {
					t.Errorf("agent.Env = %v, has unwanted env var %v", agent.Env, env)
				}
			}

			// Check agent container mounts
			for _, wantName := range test.wantAgentMounts {
				mountNameEql := func(v corev1.VolumeMount) bool {
					return v.Name == wantName
				}
				if !slices.ContainsFunc(agent.VolumeMounts, mountNameEql) {
					t.Errorf("agent.VolumeMounts = %v, missing volume mount %q", agent.VolumeMounts, wantName)
				}
			}

			// Check command container mounts
			command := findContainer(t, kjob.Spec.Template.Spec.Containers, "container-0")
			for _, wantName := range test.wantCommandMounts {
				mountNameEql := func(v corev1.VolumeMount) bool {
					return v.Name == wantName
				}
				if !slices.ContainsFunc(command.VolumeMounts, mountNameEql) {
					t.Errorf("command.VolumeMounts = %v, missing volume mount %q", command.VolumeMounts, wantName)
				}
			}
		})
	}
}

func findContainer(t *testing.T, containers []corev1.Container, name string) corev1.Container {
	t.Helper()

	for _, container := range containers {
		if container.Name == name {
			return container
		}
	}
	t.Fatal("container not found")

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
