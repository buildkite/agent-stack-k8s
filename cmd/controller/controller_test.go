package controller_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/go-cmp/cmp"
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
		PodPendingTimeout:                    15 * time.Minute,
		PollInterval:                         5 * time.Second,
		HTTPTimeout:                          1 * time.Minute,
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
		EnableCompletionWatcher:              true,
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

	cleanTestEnv(t)

	actual, err := controller.BuildConfigFromArgs([]string{"--config=../../examples/config.yaml"})
	if err != nil {
		t.Fatalf("controller.BuildConfigFromArgs([]string{\"--config=../../examples/config.yaml\"}) error = %v, want nil", err)
	}

	if diff := cmp.Diff(*actual, expected); diff != "" {
		t.Errorf("parsed config diff (-got +want):\n%s", diff)
	}
}

func TestParseAndValidateConfig_DefaultResourceClassValidation(t *testing.T) {
	tests := []struct {
		name       string
		configYAML string
		wantErr    string
	}{
		{
			name: "default references non-existent resource class",
			configYAML: `
default-resource-class-name: nonexistent
resource-classes:
  small:
    resource:
      requests:
        cpu: "100m"
`,
			wantErr: `default-resource-class-name "nonexistent" not found in resource-classes`,
		},
		{
			name: "default specified but no resource classes defined",
			configYAML: `
default-resource-class-name: small
`,
			wantErr: `default-resource-class-name "small" specified but no resource-classes defined`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanTestEnv(t)
			configFile := createTempConfigFile(t, tt.configYAML)

			_, err := controller.BuildConfigFromArgs([]string{"--config=" + configFile})
			if err == nil {
				t.Fatalf("controller.BuildConfigFromArgs([]string{\"--config=\" + configFile}) error = %v, want non-nil error", err)
			}
			if got, want := err.Error(), tt.wantErr; !strings.Contains(got, want) {
				t.Fatalf("err.Error() = %q, want containing %q", got, want)
			}
		})
	}
}

func TestConfigPrecedence(t *testing.T) {
	// Helper to build config with optional config file
	buildConfig := func(t *testing.T, args []string, configFile string) (*config.Config, error) {
		t.Helper()
		if configFile != "" {
			args = append([]string{"--config=" + configFile}, args...)
		}
		return controller.BuildConfigFromArgs(args)
	}

	t.Run("defaults applied when nothing set", func(t *testing.T) {
		cleanTestEnv(t)

		cfg, err := buildConfig(t, []string{}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.MaxInFlight, 25; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
		if got, want := cfg.Namespace, "default"; got != want {
			t.Errorf("cfg.Namespace = %q, want %q", got, want)
		}
		if got, want := cfg.JobTTL, 10*time.Minute; got != want {
			t.Errorf("cfg.JobTTL = %d, want %d", got, want)
		}
		if got, want := cfg.PodPendingTimeout, 15*time.Minute; got != want {
			t.Errorf("cfg.PodPendingTimeout = %d, want %d", got, want)
		}
		if got := cfg.Debug; got {
			t.Errorf("cfg.Debug = %t, want false", got)
		}
	})

	t.Run("config file overrides defaults", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `
max-in-flight: 100
namespace: from-file
debug: true
`)

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.MaxInFlight, 100; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
		if got, want := cfg.Namespace, "from-file"; got != want {
			t.Errorf("cfg.Namespace = %q, want %q", got, want)
		}
		if got := cfg.Debug; !got {
			t.Errorf("cfg.Debug = %t, want true", got)
		}
	})

	t.Run("config file can set zero value", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `max-in-flight: 0`)

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.MaxInFlight, 0; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
	})

	t.Run("config file can set false", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `debug: false`)

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got := cfg.Debug; got {
			t.Errorf("cfg.Debug = %t, want false", got)
		}
	})

	t.Run("env var overrides config file", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `
max-in-flight: 100
namespace: from-file
`)
		t.Setenv("MAX_IN_FLIGHT", "50")
		t.Setenv("NAMESPACE", "from-env")

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.MaxInFlight, 50; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
		if got, want := cfg.Namespace, "from-env"; got != want {
			t.Errorf("cfg.Namespace = %q, want %q", got, want)
		}
	})

	t.Run("env var can override config file with zero", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `max-in-flight: 100`)
		t.Setenv("MAX_IN_FLIGHT", "0")

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.MaxInFlight, 0; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
	})

	t.Run("env var can override config file with false", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `debug: true`)
		t.Setenv("DEBUG", "false")

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got := cfg.Debug; got {
			t.Errorf("cfg.Debug = %t, want false", got)
		}
	})

	t.Run("CLI overrides env var", func(t *testing.T) {
		cleanTestEnv(t)
		t.Setenv("MAX_IN_FLIGHT", "50")
		t.Setenv("NAMESPACE", "from-env")

		cfg, err := buildConfig(t, []string{
			"--max-in-flight=200",
			"--namespace=from-cli",
		}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\n\t\"--max-in-flight=200\",\n\t\"--namespace=from-cli\",\n}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.MaxInFlight, 200; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
		if got, want := cfg.Namespace, "from-cli"; got != want {
			t.Errorf("cfg.Namespace = %q, want %q", got, want)
		}
	})

	t.Run("CLI can override env var with zero", func(t *testing.T) {
		cleanTestEnv(t)
		t.Setenv("MAX_IN_FLIGHT", "50")

		cfg, err := buildConfig(t, []string{"--max-in-flight=0"}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--max-in-flight=0\"}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.MaxInFlight, 0; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
	})

	t.Run("CLI can override env var with false", func(t *testing.T) {
		cleanTestEnv(t)
		t.Setenv("DEBUG", "true")

		cfg, err := buildConfig(t, []string{"--debug=false"}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--debug=false\"}, %q) error = %v, want nil", "", err)
		}

		if got := cfg.Debug; got {
			t.Errorf("cfg.Debug = %t, want false", got)
		}
	})

	t.Run("full precedence chain", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `
max-in-flight: 100
namespace: from-file
job-ttl: 5m
`)
		t.Setenv("NAMESPACE", "from-env")
		t.Setenv("JOB_TTL", "15m")

		cfg, err := buildConfig(t, []string{"--namespace=from-cli"}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--namespace=from-cli\"}, %q) error = %v, want nil", configFile, err)
		}

		// max-in-flight: only in file
		if got, want := cfg.MaxInFlight, 100; got != want {
			t.Errorf("cfg.MaxInFlight = %d, want %d", got, want)
		}
		// namespace: file -> env -> cli (cli wins)
		if got, want := cfg.Namespace, "from-cli"; got != want {
			t.Errorf("cfg.Namespace = %q, want %q", got, want)
		}
		// job-ttl: file -> env (env wins)
		if got, want := cfg.JobTTL, 15*time.Minute; got != want {
			t.Errorf("cfg.JobTTL = %d, want %d", got, want)
		}
	})

	t.Run("duration fields work correctly", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `
job-ttl: 30m
poll-interval: 5s
`)

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.JobTTL, 30*time.Minute; got != want {
			t.Errorf("cfg.JobTTL = %d, want %d", got, want)
		}
		if got, want := cfg.PollInterval, 5*time.Second; got != want {
			t.Errorf("cfg.PollInterval = %d, want %d", got, want)
		}
	})

	t.Run("config file with unknown field is rejected", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `
namespace: my-namespace
unknown-field: some-value
`)

		_, err := buildConfig(t, []string{}, configFile)
		if err == nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want non-nil error", configFile, err)
		}
		if got, want := err.Error(), "unknown-field"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, want containing %q", got, want)
		}
	})

	t.Run("ID settable via CLI flag", func(t *testing.T) {
		cleanTestEnv(t)

		cfg, err := buildConfig(t, []string{"--id=my-controller"}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--id=my-controller\"}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.ID, "my-controller"; got != want {
			t.Errorf("cfg.ID = %q, want %q", got, want)
		}
	})

	t.Run("CLI can override config file tags with empty", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `
tags:
  - queue=production
  - env=prod
`)

		cfg, err := buildConfig(t, []string{"--tags="}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--tags=\"}, %q) error = %v, want nil", configFile, err)
		}

		// CLI explicitly set tags to empty, should override config file
		if got := len(cfg.Tags); got != 0 {
			t.Errorf("len(cfg.Tags) = %v, want 0", got)
		}
	})

	t.Run("reservation-expiry-seconds default is zero (use server default)", func(t *testing.T) {
		cleanTestEnv(t)

		cfg, err := buildConfig(t, []string{}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.ReservationExpirySeconds, 0; got != want {
			t.Errorf("cfg.ReservationExpirySeconds = %d, want %d", got, want)
		}
	})

	t.Run("reservation-expiry-seconds settable via config file", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `reservation-expiry-seconds: 1800`)

		cfg, err := buildConfig(t, []string{}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.ReservationExpirySeconds, 1800; got != want {
			t.Errorf("cfg.ReservationExpirySeconds = %d, want %d", got, want)
		}
	})

	t.Run("reservation-expiry-seconds settable via CLI flag", func(t *testing.T) {
		cleanTestEnv(t)

		cfg, err := buildConfig(t, []string{"--reservation-expiry-seconds=900"}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--reservation-expiry-seconds=900\"}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.ReservationExpirySeconds, 900; got != want {
			t.Errorf("cfg.ReservationExpirySeconds = %d, want %d", got, want)
		}
	})

	t.Run("reservation-expiry-seconds CLI overrides config file", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `reservation-expiry-seconds: 600`)

		cfg, err := buildConfig(t, []string{"--reservation-expiry-seconds=1800"}, configFile)
		if err != nil {
			t.Fatalf("buildConfig(t, []string{\"--reservation-expiry-seconds=1800\"}, %q) error = %v, want nil", configFile, err)
		}

		if got, want := cfg.ReservationExpirySeconds, 1800; got != want {
			t.Errorf("cfg.ReservationExpirySeconds = %d, want %d", got, want)
		}
	})

	t.Run("reservation-expiry-seconds settable via env var", func(t *testing.T) {
		cleanTestEnv(t)
		t.Setenv("RESERVATION_EXPIRY_SECONDS", "3600")

		cfg, err := buildConfig(t, []string{}, "")
		if err != nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want nil", "", err)
		}

		if got, want := cfg.ReservationExpirySeconds, 3600; got != want {
			t.Errorf("cfg.ReservationExpirySeconds = %d, want %d", got, want)
		}
	})

	t.Run("reservation-expiry-seconds exceeding max is rejected", func(t *testing.T) {
		cleanTestEnv(t)
		configFile := createTempConfigFile(t, `reservation-expiry-seconds: 3601`)

		_, err := buildConfig(t, []string{}, configFile)
		if err == nil {
			t.Fatalf("buildConfig(t, []string{}, %q) error = %v, want non-nil error", configFile, err)
		}
	})
}

// cleanTestEnv unsets environment variables that might be set in CI or .envrc
// which could pollute the test environment.
func cleanTestEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"BUILDKITE_TOKEN",
		"INTEGRATION_TEST_BUILDKITE_TOKEN",
		"IMAGE",
		"NAMESPACE",
		"AGENT_TOKEN_SECRET",
		"IMAGE_PULL_BACKOFF_GRACE_PERIOD",
		"CONFIG",
		"MAX_IN_FLIGHT",
		"DEBUG",
		"JOB_TTL",
		"BUILDKITE_K8S_STACK_CONTROLLER_ID",
		"RESERVATION_EXPIRY_SECONDS",
	} {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}
}

// createTempConfigFile creates a temporary config file with the given content
// and returns its path. The file is automatically cleaned up after the test.
func createTempConfigFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp config file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close temp config file: %v", err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}
