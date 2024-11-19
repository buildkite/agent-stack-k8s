package controller_test

import (
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func ptr[T any](v T) *T {
	return &v
}

func TestReadAndParseConfig(t *testing.T) {
	expected := config.Config{
		Debug:                        true,
		AgentTokenSecret:             "my-kubernetes-secret",
		BuildkiteToken:               "my-graphql-enabled-token",
		Image:                        "my.registry.dev/buildkite-agent:latest",
		JobTTL:                       300 * time.Second,
		ImagePullBackOffGracePeriod:  60 * time.Second,
		JobCancelCheckerPollInterval: 10 * time.Second,
		PollInterval:                 5 * time.Second,
		StaleJobDataTimeout:          10 * time.Second,
		JobCreationConcurrency:       5,
		MaxInFlight:                  100,
		Namespace:                    "my-buildkite-ns",
		Org:                          "my-buildkite-org",
		Tags:                         []string{"queue=my-queue", "priority=high"},
		ClusterUUID:                  "beefcafe-abbe-baba-abba-deedcedecade",
		ProhibitKubernetesPlugin:     true,
		GraphQLEndpoint:              "http://graphql.buildkite.localhost/v1",
		AgentConfig: &config.AgentConfig{
			Endpoint: ptr("http://agent.buildkite.localhost/v3"),
		},

		DefaultCommandParams: &config.CommandParams{
			Interposer: config.InterposerVector,
			EnvFrom: []corev1.EnvFromSource{{
				Prefix: "DEPLOY_",
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "deploy-secrets",
					},
				},
			}},
		},
		DefaultCheckoutParams: &config.CheckoutParams{
			GitCredentialsSecret: &corev1.SecretVolumeSource{
				SecretName: "my-git-credentials",
			},
			EnvFrom: []corev1.EnvFromSource{{
				Prefix: "GITHUB_",
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "github-secrets",
					},
				},
			}},
		},
		DefaultSidecarParams: &config.SidecarParams{
			EnvFrom: []corev1.EnvFromSource{{
				Prefix: "LOGGING_",
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "logging-config",
					},
				},
			}},
		},
		DefaultMetadata: config.Metadata{
			Annotations: map[string]string{
				"imageregistry": "https://hub.docker.com/",
			},
			Labels: map[string]string{
				"argocd.argoproj.io/tracking-id": "example-id-here",
			},
		},
		PodSpecPatch: &corev1.PodSpec{
			ServiceAccountName:           "buildkite-agent-sa",
			AutomountServiceAccountToken: ptr(true),
			NodeSelector: map[string]string{
				"selectors.example.com/my-selector": "example-value",
			},
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
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"cpu": resource.MustParse("1000m"),
							"mem": resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
	}

	// The buildkite token is required, but it is set from a Kubernetes secret, not the config file,
	// which is itself set from a config map that is used to create env variables in the controller
	// container. As this is required, we set it here to avoid the validation error.
	t.Setenv("BUILDKITE_TOKEN", "my-graphql-enabled-token")

	// These need to be unset to as it is set in CI which pollutes the test environment
	t.Setenv("IMAGE", "")
	t.Setenv("NAMESPACE", "")

	cmd := &cobra.Command{}
	controller.AddConfigFlags(cmd)
	v, err := controller.ReadConfigFromFileArgsAndEnv(cmd, []string{})
	require.NoError(t, err)

	// We need to read the config file from the test
	v.SetConfigFile("../../examples/config.yaml")
	require.NoError(t, v.ReadInConfig())

	actual, err := controller.ParseAndValidateConfig(v)
	require.NoError(t, err)

	if diff := cmp.Diff(*actual, expected); diff != "" {
		t.Errorf("parsed config diff (-got +want):\n%s", diff)
	}
}
