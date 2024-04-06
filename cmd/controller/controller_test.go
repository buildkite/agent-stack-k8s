package controller_test

import (
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func ptr[T any](v T) *T {
	return &v
}

func TestReadAndParseConfig(t *testing.T) {
	expected := config.Config{
		Debug:            true,
		AgentTokenSecret: "my-kubernetes-secret",
		BuildkiteToken:   "my-graphql-enabled-token",
		Image:            "my.registry.dev/buildkite-agent:latest",
		JobTTL:           300 * time.Second,
		MaxInFlight:      100,
		Namespace:        "my-buildkite-ns",
		Org:              "my-buildkite-org",
		Tags:             []string{"queue=my-queue", "priority=high"},
		ClusterUUID:      "beefcafe-abbe-baba-abba-deedcedecade",
		PodSpecPatch: &corev1.PodSpec{
			ServiceAccountName:           "buildkite-agent-sa",
			AutomountServiceAccountToken: ptr(true),
			Containers: []corev1.Container{
				{
					Name: "container-0",
					Env: []corev1.EnvVar{
						{
							Name: "GITHUB_TOKEN",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "github-secrets",
									},
									Key: "github-token",
								},
							},
						},
					},
				},
			},
		},
	}

	/ The buildkite token is required, but it is set from a Kubernetes secret, not the config file,
	/ which is itself set from a config map that is used to create env variables in the controller
	// container. As this is required, we set it here to avoid the validation error.
	t.Setenv("BUILDKITE_TOKEN", "my-graphql-enabled-token")

	// This needs to be unset to as it is set in CI which pollutes the test environment
	t.Setenv("IMAGE", "")

	cmd := &cobra.Command{}
	controller.AddConfigFlags(cmd)
	v, err := controller.ReadConfigFromFileArgsAndEnv(cmd, []string{})
	require.NoError(t, err)

	// We need to read the config file from the test
	v.SetConfigFile("../../examples/config.yaml")
	require.NoError(t, v.ReadInConfig())

	actual, err := controller.ParseAndValidateConfig(v)
	require.NoError(t, err)
	require.Equal(t, expected, *actual)
}
