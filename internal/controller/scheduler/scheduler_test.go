package scheduler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
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
	if err != nil {
		t.Fatalf("json.Marshal([]map[string]any{\n\t{\n\t\t\"github.com/buildkite-plugins/kubernetes-buildkite-plugin\": pluginConfig,\n\t},\n\t{\n\t\t\"github.com/buildkite-plugins/some-other-buildkite-plugin\": map[string]any{\n\t\t\t\"foo\": \"bar\",\n\t\t},\n\t},\n}) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(
		slog.Default(),
		nil,
		nil,
		Config{
			AgentTokenSecretName: "token-secret",
			Image:                "buildkite/agent:latest",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(pluginConfig.PodSpec, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(pluginConfig.PodSpec, %t, inputs) error = %v, want nil", false, err)
	}

	gotPodSpec := kjob.Spec.Template.Spec

	if got, want := len(gotPodSpec.Containers), 3; got != want {
		t.Errorf("len(gotPodSpec.Containers) = %d, want %d", got, want)
	}

	agentContainer := findContainer(t, gotPodSpec.Containers, "agent")

	tokenEnv := findEnv(t, agentContainer.Env, "BUILDKITE_AGENT_TOKEN")
	if got, want := tokenEnv.ValueFrom.SecretKeyRef.Name, "token-secret"; got != want {
		t.Errorf("tokenEnv.ValueFrom.SecretKeyRef.Name = %q, want %q", got, want)
	}

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
	if diff := cmp.Diff(slices.Sorted(slices.Values(envFromNames)), slices.Sorted(slices.Values([]string{"some-configmap", "git-secret"}))); diff != "" {
		t.Fatalf("envFromNames sorted diff (-got +want):\n%s", diff)
	}

	tagLabel := kjob.Labels["tag.buildkite.com/queue"]
	if got, want := tagLabel, "kubernetes"; got != want {
		t.Errorf("kjob.Labels[\"tag.buildkite.com/queue\"] = %q, want %q", got, want)
	}
}

func TestTagEnv(t *testing.T) {
	t.Parallel()
	logger := slog.Default()

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
	if err != nil {
		t.Fatalf("json.Marshal([]map[string]any{\n\t{\n\t\t\"github.com/buildkite-plugins/kubernetes-buildkite-plugin\": pluginConfig,\n\t},\n}) error = %v, want nil", err)
	}

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
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(pluginConfig.PodSpec, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(pluginConfig.PodSpec, %t, inputs) error = %v, want nil", false, err)
	}

	container := findContainer(t, kjob.Spec.Template.Spec.Containers, "agent")
	assertEnvFieldPath(t, container, "BUILDKITE_K8S_NODE", "spec.nodeName")
	assertEnvFieldPath(t, container, "BUILDKITE_K8S_NAMESPACE", "metadata.namespace")
	assertEnvFieldPath(t, container, "BUILDKITE_K8S_SERVICE_ACCOUNT", "spec.serviceAccountName")
}

func assertEnvFieldPath(t *testing.T, container corev1.Container, envVarName, fieldPath string) {
	t.Helper()

	if got, want := countEnv(container.Env, envVarName), 1; got != want {
		t.Errorf("%s env count = %d, want %d", envVarName, got, want)
		if got == 0 {
			return
		}
	}
	env := findEnv(t, container.Env, envVarName)
	if got, want := env.Value, ""; got != want {
		t.Errorf("env.Value = %q, want %q", got, want)
	}
	if got := env.ValueFrom; got == nil {
		t.Errorf("env.ValueFrom = %v, want non-nil value", got)
		return
	}
	if got := env.ValueFrom.FieldRef; got == nil {
		t.Errorf("env.ValueFrom.FieldRef = %v, want non-nil value", got)
		return
	}
	if got, want := env.ValueFrom.FieldRef.FieldPath, fieldPath; got != want {
		t.Errorf("fieldPath = %q, want %q", got, want)
	}
}

func TestJobWithNoKubernetesPlugin(t *testing.T) {
	t.Parallel()
	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := len(kjob.Spec.Template.Spec.Containers), 3; got != want {
		t.Fatalf("len(kjob.Spec.Template.Spec.Containers) = %d, want %d", got, want)
	}

	commandContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, "container-0")
	commandEnv := findEnv(t, commandContainer.Env, "BUILDKITE_COMMAND")
	if got, want := commandEnv.Value, job.Command; got != want {
		t.Fatalf("commandEnv.Value = %q, want %q", got, want)
	}
	pluginsEnv := findEnv(t, commandContainer.Env, "BUILDKITE_PLUGINS")
	if got := pluginsEnv; got != nil {
		t.Fatalf("findEnv(t, commandContainer.Env, %q) = %v, want nil", "BUILDKITE_PLUGINS", got)
	}
}

func TestBuild(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    podSpecPatch:
      containers:
      - name: container-0
        image: alpine:latest`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		slog.Default(),
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
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := len(kjob.Spec.Template.Spec.Containers), 3; got != want {
		t.Fatalf("len(kjob.Spec.Template.Spec.Containers) = %d, want %d", got, want)
	}

	controllerIDLabel := kjob.Labels["buildkite.com/controller-id"]
	if got, want := controllerIDLabel, "controller-1"; got != want {
		t.Errorf("kjob.Labels[\"buildkite.com/controller-id\"] = %q, want %q", got, want)
	}

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

func TestBuildStepKeyAnnotationCannotBeOverriddenByPlugin(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    metadata:
      annotations:
        buildkite.com/step-key: forged-step`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env: map[string]string{
			"BUILDKITE_PLUGINS":  string(pluginsJSON),
			"BUILDKITE_STEP_KEY": "trusted-step",
		},
	}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, &api.AgentScheduledJob{})
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	const annotation = "buildkite.com/step-key"
	if got, want := kjob.Annotations[annotation], "trusted-step"; got != want {
		t.Errorf("kjob.Annotations[%q] = %q, want %q", annotation, got, want)
	}
	if got, want := kjob.Spec.Template.Annotations[annotation], "trusted-step"; got != want {
		t.Errorf("kjob.Spec.Template.Annotations[%q] = %q, want %q", annotation, got, want)
	}
}

func TestBuildWorkspaceMountSubPathExpr(t *testing.T) {
	t.Parallel()

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(
		slog.Default(),
		nil,
		nil,
		Config{
			Namespace:                 "buildkite",
			Image:                     "buildkite/agent:latest",
			AgentTokenSecretName:      "bkcq_1234567890",
			WorkspaceMountSubPathExpr: "$(POD_NAME)",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	inputs.k8sPlugin = &KubernetesPlugin{
		Sidecars: []corev1.Container{{
			Name:  "custom-sidecar",
			Image: "busybox:latest",
		}},
	}
	kjob, err := worker.Build(&corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:  "custom-command",
			Image: "alpine:latest",
		}},
	}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(podSpec, %t, inputs) error = %v, want nil", false, err)
	}

	const wantMountPath = "/workspace"
	const wantSubPathExpr = "$(POD_NAME)"

	checkWorkspaceMount := func(t *testing.T, label string, container corev1.Container) {
		t.Helper()
		var found bool
		for _, m := range container.VolumeMounts {
			if m.MountPath != wantMountPath {
				continue
			}
			found = true
			if m.SubPathExpr != wantSubPathExpr {
				t.Errorf("%s container %q: workspace mount SubPathExpr = %q, want %q",
					label, container.Name, m.SubPathExpr, wantSubPathExpr)
			}
		}
		if !found {
			t.Errorf("%s container %q: no /workspace mount found", label, container.Name)
		}
		assertEnvFieldPath(t, container, "POD_NAME", "metadata.name")
	}

	for _, c := range kjob.Spec.Template.Spec.Containers {
		checkWorkspaceMount(t, "container", c)
	}
	var foundImageCheck bool
	for _, c := range kjob.Spec.Template.Spec.InitContainers {
		checkWorkspaceMount(t, "initContainer", c)
		foundImageCheck = foundImageCheck || strings.HasPrefix(c.Name, ImageCheckContainerNamePrefix)
	}
	if !foundImageCheck {
		t.Error("kjob.Spec.Template.Spec.InitContainers has no image-check container")
	}
}

func TestBuildWorkspaceMountSubPathExprAfterPodSpecPatches(t *testing.T) {
	t.Parallel()

	worker := New(slog.Default(), nil, nil, Config{
		Image:                     "buildkite/agent:latest",
		WorkspaceMountSubPathExpr: "$(POD_NAME)",
		SkipImageCheckContainers:  true,
		PodSpecPatch: &corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:         "controller-patched",
				Image:        "alpine:latest",
				VolumeMounts: workspaceVolumeMounts("$(POD_NAME)"),
				Env: []corev1.EnvVar{{
					Name:  "POD_NAME",
					Value: "controller-value",
				}},
			}},
		},
	})

	kjob, err := worker.Build(&corev1.PodSpec{}, false, buildInputs{
		uuid:    "abc",
		command: "echo hello world",
		k8sPlugin: &KubernetesPlugin{
			PodSpecPatch: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:         DefaultCommandContainerName,
						VolumeMounts: workspaceVolumeMounts("static"),
					},
					{
						Name: "controller-patched",
						Env: []corev1.EnvVar{{
							Name:  "POD_NAME",
							Value: "plugin-value",
						}},
					},
					{
						Name:         "plugin-patched",
						Image:        "busybox:latest",
						VolumeMounts: workspaceVolumeMounts("$(POD_NAME)"),
						EnvFrom: []corev1.EnvFromSource{{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "pod-config"},
							},
						}},
					},
					{
						Name:  "other-mount-path",
						Image: "busybox:latest",
						VolumeMounts: []corev1.VolumeMount{{
							Name:        "workspace",
							MountPath:   "/other",
							SubPathExpr: "$(POD_NAME)",
						}},
					},
				},
				InitContainers: []corev1.Container{{
					Name:         "plugin-patched-init",
					Image:        "busybox:latest",
					VolumeMounts: workspaceVolumeMounts("pods/$(POD_NAME)"),
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("worker.Build() error = %v, want nil", err)
	}

	controllerPatched := findContainer(t, kjob.Spec.Template.Spec.Containers, "controller-patched")
	wantPodName := &corev1.EnvVar{Name: "POD_NAME", Value: "plugin-value"}
	if diff := cmp.Diff(findEnv(t, controllerPatched.Env, "POD_NAME"), wantPodName); diff != "" {
		t.Errorf("controller-patched POD_NAME diff (-got +want):\n%s", diff)
	}
	if got, want := countEnv(controllerPatched.Env, "POD_NAME"), 1; got != want {
		t.Errorf("controller-patched POD_NAME env count = %d, want %d", got, want)
	}

	pluginPatched := findContainer(t, kjob.Spec.Template.Spec.Containers, "plugin-patched")
	assertEnvFieldPath(t, pluginPatched, "POD_NAME", "metadata.name")
	if got, want := len(pluginPatched.EnvFrom), 1; got != want {
		t.Errorf("plugin-patched EnvFrom count = %d, want %d", got, want)
	}

	pluginPatchedInit := findContainer(t, kjob.Spec.Template.Spec.InitContainers, "plugin-patched-init")
	assertEnvFieldPath(t, pluginPatchedInit, "POD_NAME", "metadata.name")

	otherMountPath := findContainer(t, kjob.Spec.Template.Spec.Containers, "other-mount-path")
	if got := findEnv(t, otherMountPath.Env, "POD_NAME"); got != nil {
		t.Errorf("other-mount-path POD_NAME = %v, want nil", got)
	}

	defaultCommand := findContainer(t, kjob.Spec.Template.Spec.Containers, DefaultCommandContainerName)
	if got := findEnv(t, defaultCommand.Env, "POD_NAME"); got != nil {
		t.Errorf("default command POD_NAME = %v, want nil after plugin replaced SubPathExpr", got)
	}
}

func TestSubPathExprExpandsPodName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{name: "whole expression", expr: "$(POD_NAME)", want: true},
		{name: "embedded expression", expr: "pods/$(POD_NAME)/workspace", want: true},
		{name: "after another reference", expr: "$(OTHER)/$(POD_NAME)", want: true},
		{name: "odd dollar run", expr: "$$$(POD_NAME)", want: true},
		{name: "backslash is not an escape", expr: `\$(POD_NAME)`, want: true},
		{name: "after non-ASCII rune", expr: "$£$(POD_NAME)", want: true},
		{name: "empty", expr: "", want: false},
		{name: "static", expr: "workspace", want: false},
		{name: "other reference", expr: "$(OTHER)", want: false},
		{name: "escaped reference", expr: "$$(POD_NAME)", want: false},
		{name: "shell reference", expr: "${POD_NAME}", want: false},
		{name: "bare reference", expr: "$POD_NAME", want: false},
		{name: "unterminated reference", expr: "$(POD_NAME", want: false},
		{name: "nested reference", expr: "$($(POD_NAME))", want: false},
		{name: "different name prefix", expr: "$(POD_NAME_SUFFIX)", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := subPathExprExpandsPodName(test.expr); got != test.want {
				t.Errorf("subPathExprExpandsPodName(%q) = %t, want %t", test.expr, got, test.want)
			}
		})
	}
}

// TestBuildWorkspaceMountSubPathExprDefault verifies the previous behavior
// (no SubPathExpr) is preserved when the new field is unset.
func TestBuildWorkspaceMountSubPathExprDefault(t *testing.T) {
	t.Parallel()

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	checkNoSubPath := func(t *testing.T, label, containerName string, mounts []corev1.VolumeMount) {
		t.Helper()
		for _, m := range mounts {
			if m.MountPath == "/workspace" && m.SubPathExpr != "" {
				t.Errorf("%s container %q: workspace mount SubPathExpr = %q, want empty",
					label, containerName, m.SubPathExpr)
			}
		}
	}
	for _, c := range kjob.Spec.Template.Spec.Containers {
		checkNoSubPath(t, "container", c.Name, c.VolumeMounts)
	}
	for _, c := range kjob.Spec.Template.Spec.InitContainers {
		checkNoSubPath(t, "initContainer", c.Name, c.VolumeMounts)
	}
}

func TestBuildSkipCheckout(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    checkout:
      skip: true`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		slog.Default(),
		nil,
		nil,
		Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := len(kjob.Spec.Template.Spec.Containers), 2; got != want {
		t.Fatalf("len(kjob.Spec.Template.Spec.Containers) = %d, want %d", got, want)
	}

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
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		slog.Default(),
		nil,
		nil,
		Config{
			Namespace:            "buildkite",
			Image:                "buildkite/agent:latest",
			AgentTokenSecretName: "bkcq_1234567890",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

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
	worker := New(slog.Default(), nil, nil, Config{
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
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	var checkoutContainer *corev1.Container
	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			checkoutContainer = &container
		}
	}

	if got := checkoutContainer; got == nil {
		t.Fatalf("&container = %v, want non-nil value", got)
	}

	// Validate that git credential secret is mounted and available in checkout container's path
	var hasGitCredentialsRO bool
	for _, mount := range checkoutContainer.VolumeMounts {
		if mount.Name == "git-credentials-ro" && mount.MountPath == "/buildkite/git-credentials-ro" {
			hasGitCredentialsRO = true
		}
	}

	if !hasGitCredentialsRO {
		t.Error("checkout container missing git-credentials-ro volume mount at /buildkite/git-credentials-ro")
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

func TestCheckoutGitCredentialsHelperEscaping(t *testing.T) {
	t.Parallel()

	podUser := int64(1000)
	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
		DefaultCheckoutParams: &config.CheckoutParams{
			GitCredentialsSecret: &corev1.SecretVolumeSource{
				SecretName: "bluh",
			},
		},
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	podSpec := &corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{
			RunAsUser: &podUser,
		},
	}
	kjob, err := worker.Build(podSpec, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(podSpec, %t, inputs) error = %v, want nil", false, err)
	}

	var checkoutContainer *corev1.Container
	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			checkoutContainer = &container
		}
	}
	if checkoutContainer == nil {
		t.Fatal("checkout container not found")
	}
	if len(checkoutContainer.Args) != 1 {
		t.Fatalf("checkout container Args = %v, want single shell script arg", checkoutContainer.Args)
	}

	checkoutScript := checkoutContainer.Args[0]
	if !strings.Contains(checkoutScript, `\$1`) {
		t.Error("checkout script missing escaped \\$1 in git credential helper for su -c path")
	}
	if strings.Contains(checkoutScript, `"$1"`) {
		t.Error("checkout script contains unescaped $1 in git credential helper; outer shell would expand it inside su -c")
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
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}

	worker := New(
		slog.Default(),
		nil,
		nil,
		Config{
			Namespace: "buildkite",
			Image:     "buildkite/agent:latest",
		},
	)
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	var checkoutContainer *corev1.Container
	for _, container := range kjob.Spec.Template.Spec.Containers {
		if container.Name == "checkout" {
			checkoutContainer = &container
		}
	}

	if got := checkoutContainer; got == nil {
		t.Fatalf("&container = %v, want non-nil value", got)
	}

	// Validate that git credential secret is mounted and available in checkout container's path
	var hasGitCredentialsRO bool
	for _, mount := range checkoutContainer.VolumeMounts {
		if mount.Name == "git-credentials-ro" && mount.MountPath == "/buildkite/git-credentials-ro" {
			hasGitCredentialsRO = true
		}
	}

	if !hasGitCredentialsRO {
		t.Error("checkout container missing git-credentials-ro volume mount at /buildkite/git-credentials-ro")
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
	if err != nil {
		t.Fatalf("json.Marshal([]map[string]any{\n\t{\n\t\t\"github.com/buildkite-plugins/kubernetes-buildkite-plugin\": `\"some-invalid-json\"`,\n\t},\n}) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	wrapper := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	_, err = wrapper.ParseJob(job, sjob)
	if err == nil {
		t.Fatalf("wrapper.ParseJob(job, sjob) error = %v, want non-nil error", err)
	}
}

func TestProhibitKubernetesPlugin(t *testing.T) {
	t.Parallel()
	pluginsJSON, err := json.Marshal([]map[string]any{
		{
			"github.com/buildkite-plugins/kubernetes-buildkite-plugin": KubernetesPlugin{},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal([]map[string]any{\n\t{\n\t\t\"github.com/buildkite-plugins/kubernetes-buildkite-plugin\": KubernetesPlugin{},\n\t},\n}) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:  "abc",
		Env: map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(slog.Default(), nil, nil, Config{
		Image:             "buildkite/agent:latest",
		ProhibitK8sPlugin: true,
	})
	_, err = worker.ParseJob(job, sjob)
	if err == nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want non-nil error", err)
	}
}

func TestCustomImageSyntax_pluginTakesTopPriority(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    podSpecPatch:
      containers:
      - name: container-0
        image: "x-image:plugin"`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

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
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	commandContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, DefaultCommandContainerName)
	if got, want := commandContainer.Image, "x-image:plugin"; got != want {
		t.Fatalf("commandContainer.Image = %q, want %q", got, want)
	}
}

// Job level image syntax takes priority over controller setting
func TestCustomImageSyntax_jobLevelImagePriority(t *testing.T) {
	t.Parallel()

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello",
		Env: map[string]string{
			"BUILDKITE_IMAGE": "x-image:job",
		},
	}
	sjob := &api.AgentScheduledJob{
		AgentQueryRules: []string{"queue=kubernetes"},
	}
	worker := New(slog.Default(), nil, nil, Config{
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
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	commandContainer := findContainer(t, kjob.Spec.Template.Spec.Containers, DefaultCommandContainerName)

	if got, want := commandContainer.Image, "x-image:job"; got != want {
		t.Fatalf("commandContainer.Image = %q, want %q", got, want)
	}
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
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
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
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
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
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
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
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
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
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
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
				"agent":       corev1.PullIfNotPresent,
				"checkout":    corev1.PullIfNotPresent,
				"copy-agent":  corev1.PullAlways,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			worker := New(
				slog.Default(),
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

func TestAdditionalHooksOptions(t *testing.T) {
	additionalHooksVolume := &corev1.Volume{
		Name: "additional-hooks",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}

	tests := []struct {
		name                     string
		agentConfig              *config.AgentConfig
		wantAdditionalHooksEnv   string
		wantPodVolumes           []string
		wantNotPodVolumes        []string
		wantAdditionalHookMounts map[string]string
	}{
		{
			name:                   "sets additional hooks env when only image-baked paths are configured",
			agentConfig:            &config.AgentConfig{AdditionalHooksPaths: []string{"/image/hooks", "/image/more-hooks"}},
			wantAdditionalHooksEnv: "/image/hooks,/image/more-hooks",
			wantNotPodVolumes: []string{
				additionalHooksVolume.Name,
			},
		},
		{
			name: "mounts volume-backed additional hooks and adds their path to the env",
			agentConfig: &config.AgentConfig{
				AdditionalHooks: []config.AdditionalHook{
					{
						Path:   "/buildkite/additional-hooks",
						Volume: additionalHooksVolume,
					},
				},
			},
			wantAdditionalHooksEnv: "/buildkite/additional-hooks",
			wantPodVolumes:         []string{additionalHooksVolume.Name},
			wantAdditionalHookMounts: map[string]string{
				additionalHooksVolume.Name: "/buildkite/additional-hooks",
			},
		},
		{
			name: "combines image-baked and volume-backed additional hook paths",
			agentConfig: &config.AgentConfig{
				AdditionalHooksPaths: []string{"/image/hooks"},
				AdditionalHooks: []config.AdditionalHook{
					{
						Path:   "/buildkite/additional-hooks",
						Volume: additionalHooksVolume,
					},
				},
			},
			wantAdditionalHooksEnv: "/image/hooks,/buildkite/additional-hooks",
			wantPodVolumes:         []string{additionalHooksVolume.Name},
			wantAdditionalHookMounts: map[string]string{
				additionalHooksVolume.Name: "/buildkite/additional-hooks",
			},
		},
		{
			name:              "does not set additional hook config when agent config is nil",
			agentConfig:       nil,
			wantNotPodVolumes: []string{additionalHooksVolume.Name},
		},
		{
			name:              "does not set additional hook config when agent config is empty",
			agentConfig:       &config.AgentConfig{},
			wantNotPodVolumes: []string{additionalHooksVolume.Name},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			worker := New(
				slog.Default(),
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
					agentQueryRules: []string{"queue=kubernetes"},
				},
			)
			if err != nil {
				t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, buildInputs{\n\tuuid:\t\t\t\"1234\",\n\tcommand:\t\t\"echo shell\",\n\tagentQueryRules:\t[]string{\"queue=kubernetes\"},\n}) error = %v, want nil", false, err)
			}

			// Check pod volumes.
			podSpec := kjob.Spec.Template.Spec
			for _, wantName := range test.wantPodVolumes {
				if !hasVolumeNamed(podSpec.Volumes, wantName) {
					t.Errorf("podSpec.Volumes = %v, is missing volume named %q", podSpec.Volumes, wantName)
				}
			}
			for _, wantNotName := range test.wantNotPodVolumes {
				if hasVolumeNamed(podSpec.Volumes, wantNotName) {
					t.Errorf("podSpec.Volumes = %v, has unwanted volume named %q", podSpec.Volumes, wantNotName)
				}
			}

			// Check agent env.
			agent := findContainer(t, podSpec.Containers, "agent")
			env := findEnv(t, agent.Env, "BUILDKITE_ADDITIONAL_HOOKS_PATHS")
			if test.wantAdditionalHooksEnv == "" {
				if env != nil {
					t.Errorf("agent.Env = %v, has unwanted BUILDKITE_ADDITIONAL_HOOKS_PATHS env var", agent.Env)
				}
			} else if env == nil || env.Value != test.wantAdditionalHooksEnv {
				t.Errorf("agent.Env[BUILDKITE_ADDITIONAL_HOOKS_PATHS] = %v, want %q", env, test.wantAdditionalHooksEnv)
			}

			// Check container mounts.
			containers := []corev1.Container{
				agent,
				findContainer(t, podSpec.Containers, "checkout"),
				findContainer(t, podSpec.Containers, "container-0"),
			}
			for _, ctr := range containers {
				for _, wantNotName := range test.wantNotPodVolumes {
					if hasMountNamed(ctr.VolumeMounts, wantNotName) {
						t.Errorf("%s.VolumeMounts = %v, has unwanted mount %q", ctr.Name, ctr.VolumeMounts, wantNotName)
					}
				}
				for wantName, wantPath := range test.wantAdditionalHookMounts {
					if !hasMountAt(ctr.VolumeMounts, wantName, wantPath) {
						t.Errorf("%s.VolumeMounts = %v, missing mount %q at %q", ctr.Name, ctr.VolumeMounts, wantName, wantPath)
					}
				}
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
				VerificationJWKSFile:   new("/path/to/verification.jwks"),
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
				SigningJWKSFile:   new("/path/to/signing.jwks"),
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
				VerificationJWKSFile:   new("/verification/path/verification.jwks"),
				VerificationJWKSVolume: verificationVol,
				SigningJWKSFile:        new("/signing/path/signing.jwks"),
				SigningJWKSVolume:      signingVol,
			},
			wantPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE":      "/signing/path/signing.jwks",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE": "/verification/path/verification.jwks",
			},
			wantAgentMounts:   []string{verificationVol.Name, signingVol.Name},
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
			wantAgentMounts:   []string{verificationVol.Name, signingVol.Name},
			wantCommandMounts: []string{signingVol.Name},
		},
		{
			name: "relative paths",
			agentConfig: &config.AgentConfig{
				VerificationJWKSVolume: verificationVol,
				VerificationJWKSFile:   new("my-awesome-key"),
				SigningJWKSVolume:      signingVol,
				SigningJWKSFile:        new("my-special-key"),
			},
			wantPodVolumes: []string{verificationVol.Name, signingVol.Name},
			wantAgentEnv: map[string]string{
				"BUILDKITE_AGENT_SIGNING_JWKS_FILE":      "/buildkite/signing-jwks/my-special-key",
				"BUILDKITE_AGENT_VERIFICATION_JWKS_FILE": "/buildkite/verification-jwks/my-awesome-key",
			},
			wantAgentMounts:   []string{verificationVol.Name, signingVol.Name},
			wantCommandMounts: []string{signingVol.Name},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			worker := New(
				slog.Default(),
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
				if !hasVolumeNamed(podSpec.Volumes, wantName) {
					t.Errorf("podSpec.Volumes = %v, is missing volume named %q", podSpec.Volumes, wantName)
				}
			}
			for _, wantNotName := range test.wantNotPodVolumes {
				if hasVolumeNamed(podSpec.Volumes, wantNotName) {
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
				if !hasMountNamed(agent.VolumeMounts, wantName) {
					t.Errorf("agent.VolumeMounts = %v, missing volume mount %q", agent.VolumeMounts, wantName)
				}
			}

			// Check command container mounts
			command := findContainer(t, kjob.Spec.Template.Spec.Containers, "container-0")
			for _, wantName := range test.wantCommandMounts {
				if !hasMountNamed(command.VolumeMounts, wantName) {
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

func countEnv(envs []corev1.EnvVar, name string) int {
	var count int
	for _, env := range envs {
		if env.Name == name {
			count++
		}
	}
	return count
}

func workspaceVolumeMounts(subPathExpr string) []corev1.VolumeMount {
	return []corev1.VolumeMount{{
		Name:        "workspace",
		MountPath:   "/workspace",
		SubPathExpr: subPathExpr,
	}}
}

func hasVolumeNamed(volumes []corev1.Volume, name string) bool {
	for _, volume := range volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

func hasMountNamed(mounts []corev1.VolumeMount, name string) bool {
	for _, mount := range mounts {
		if mount.Name == name {
			return true
		}
	}
	return false
}

func hasMountAt(mounts []corev1.VolumeMount, name, path string) bool {
	for _, mount := range mounts {
		if mount.Name == name && mount.MountPath == path {
			return true
		}
	}
	return false
}

func TestBuildSafeToEvictDefault(t *testing.T) {
	t.Parallel()
	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := kjob.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"], "false"; got != want {
		t.Errorf("kjob.Annotations[\"cluster-autoscaler.kubernetes.io/safe-to-evict\"] = %q, want %q", got, want)
	}
	if got, want := kjob.Spec.Template.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"], "false"; got != want {
		t.Errorf("kjob.Spec.Template.Annotations[\"cluster-autoscaler.kubernetes.io/safe-to-evict\"] = %q, want %q", got, want)
	}
}

func TestBuildSafeToEvictDefaultMetadataOverride(t *testing.T) {
	t.Parallel()
	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
		DefaultMetadata: config.Metadata{
			Annotations: map[string]string{
				"cluster-autoscaler.kubernetes.io/safe-to-evict": "true",
			},
		},
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := kjob.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"], "true"; got != want {
		t.Errorf("kjob.Annotations[\"cluster-autoscaler.kubernetes.io/safe-to-evict\"] = %q, want %q", got, want)
	}
	if got, want := kjob.Spec.Template.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"], "true"; got != want {
		t.Errorf("kjob.Spec.Template.Annotations[\"cluster-autoscaler.kubernetes.io/safe-to-evict\"] = %q, want %q", got, want)
	}
}

func TestBuildSafeToEvictPluginMetadataOverride(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    metadata:
      annotations:
        cluster-autoscaler.kubernetes.io/safe-to-evict: "true"`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := kjob.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"], "true"; got != want {
		t.Errorf("kjob.Annotations[\"cluster-autoscaler.kubernetes.io/safe-to-evict\"] = %q, want %q", got, want)
	}
	if got, want := kjob.Spec.Template.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"], "true"; got != want {
		t.Errorf("kjob.Spec.Template.Annotations[\"cluster-autoscaler.kubernetes.io/safe-to-evict\"] = %q, want %q", got, want)
	}
}

func TestBuildPodTemplateAnnotation(t *testing.T) {
	t.Parallel()

	pluginsYAML := `- github.com/buildkite-plugins/kubernetes-buildkite-plugin:
    podTemplate: my-pod-template`

	pluginsJSON, err := yaml.YAMLToJSONStrict([]byte(pluginsYAML))
	if err != nil {
		t.Fatalf("yaml.YAMLToJSONStrict([]byte(pluginsYAML)) error = %v, want nil", err)
	}

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
		Env:     map[string]string{"BUILDKITE_PLUGINS": string(pluginsJSON)},
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got, want := kjob.Annotations[config.PodTemplateAnnotation], "my-pod-template"; got != want {
		t.Fatalf("kjob.Annotations[config.PodTemplateAnnotation] = %q, want %q", got, want)
	}
	if got, want := kjob.Spec.Template.Annotations[config.PodTemplateAnnotation], "my-pod-template"; got != want {
		t.Fatalf("kjob.Spec.Template.Annotations[config.PodTemplateAnnotation] = %q, want %q", got, want)
	}
}

func TestBuildNoPodTemplateAnnotationWhenAbsent(t *testing.T) {
	t.Parallel()

	job := &api.AgentJob{
		ID:      "abc",
		Command: "echo hello world",
	}
	sjob := &api.AgentScheduledJob{}
	worker := New(slog.Default(), nil, nil, Config{
		Image: "buildkite/agent:latest",
	})
	inputs, err := worker.ParseJob(job, sjob)
	if err != nil {
		t.Fatalf("worker.ParseJob(job, sjob) error = %v, want nil", err)
	}
	kjob, err := worker.Build(&corev1.PodSpec{}, false, inputs)
	if err != nil {
		t.Fatalf("worker.Build(&corev1.PodSpec{}, %t, inputs) error = %v, want nil", false, err)
	}

	if got := kjob.Annotations[config.PodTemplateAnnotation]; got != "" {
		t.Fatalf("kjob.Annotations[config.PodTemplateAnnotation] = %v, want \"\"", got)
	}
	if got := kjob.Spec.Template.Annotations[config.PodTemplateAnnotation]; got != "" {
		t.Fatalf("kjob.Spec.Template.Annotations[config.PodTemplateAnnotation] = %v, want \"\"", got)
	}
}
