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
		Debug:                                true,
		AgentTokenSecret:                     "my-kubernetes-secret",
		BuildkiteToken:                       "my-graphql-enabled-token",
		Image:                                "my.registry.dev/buildkite-agent:latest",
		JobTTL:                               300 * time.Second,
		JobActiveDeadlineSeconds:             21600,
		DefaultTerminationGracePeriodSeconds: 80,
		JobPrefix:                            "testkite-",
		ImagePullBackOffGracePeriod:          60 * time.Second,
		JobCancelCheckerPollInterval:         10 * time.Second,
		EmptyJobGracePeriod:                  50 * time.Second,
		PollInterval:                         5 * time.Second,
		JobCreationConcurrency:               5,
		MaxInFlight:                          100,
		K8sClientRateLimiterQPS:              20,
		K8sClientRateLimiterBurst:            30,
		Namespace:                            "my-buildkite-ns",
		Queue:                                "my-queue",
		Tags:                                 []string{"priority=high"},
		PrometheusPort:                       9216,
		ProhibitKubernetesPlugin:             true,
		PaginationPageSize:                   1000,
		PaginationDepthLimit:                 5,
		QueryResetInterval:                   10 * time.Second,
		DefaultImagePullPolicy:               "Never",
		DefaultImageCheckPullPolicy:          "IfNotPresent",
		EnableQueuePause:                     true,
		WorkQueueLimit:                       2_000_000,
		ImageCheckContainerCPULimit:          "201m",
		ImageCheckContainerMemoryLimit:       "129Mi",
		LogFormat:                            "logfmt",
		LogLevel:                             "info",
		NoColor:                              false,

		ResourceClasses: map[string]*config.ResourceClass{
			"small": {
				Resource: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						"cpu":           resource.MustParse("500m"),
						"memory":        resource.MustParse("512Mi"),
						"hugepages-2Mi": resource.MustParse("1Mi"),
					},
				},
			},
		},
		DefaultResourceClassName: "small",

		WorkspaceVolume: &corev1.Volume{
			Name: "workspace-2-the-reckoning",
			VolumeSource: corev1.VolumeSource{
				Ephemeral: &corev1.EphemeralVolumeSource{
					VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							StorageClassName: ptr("my-special-storage-class"),
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									"storage": resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
		},
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
					Name:  "container-0",
					Image: "example.org/my-container@latest",
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
							"cpu":           resource.MustParse("1000m"),
							"mem":           resource.MustParse("4Gi"),
							"hugepages-2Mi": resource.MustParse("2Mi"),
						},
					},
				},
			},
		},
	}

	// These need to be unset to as it is set in CI which pollutes the test environment
	t.Setenv("INTEGRATION_TEST_BUILDKITE_TOKEN", "")
	t.Setenv("IMAGE", "")
	t.Setenv("NAMESPACE", "")
	t.Setenv("AGENT_TOKEN_SECRET", "")

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
