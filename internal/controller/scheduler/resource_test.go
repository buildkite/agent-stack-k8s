package scheduler

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestApplyResourceClass(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	smallResourceClass := &config.ResourceClass{
		Resource: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
	}

	largeResourceClass := &config.ResourceClass{
		Resource: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}

	commandContainerEnv := []corev1.EnvVar{
		{Name: "BUILDKITE_BOOTSTRAP_PHASES", Value: "plugin,command"},
		{Name: "BUILDKITE_COMMAND", Value: "echo hello"},
	}

	tests := []struct {
		name                     string
		resourceClasses          map[string]*config.ResourceClass
		defaultResourceClassName string
		tags                     map[string]string
		wantErr                  string
		wantResources            *corev1.ResourceRequirements
	}{
		{
			name:                     "no tag and no default - no resources applied",
			resourceClasses:          map[string]*config.ResourceClass{"small": smallResourceClass},
			defaultResourceClassName: "",
			tags:                     map[string]string{"queue": "test"},
			wantErr:                  "",
			wantResources:            nil,
		},
		{
			name:                     "tag specified - applies tagged resource class",
			resourceClasses:          map[string]*config.ResourceClass{"small": smallResourceClass, "large": largeResourceClass},
			defaultResourceClassName: "",
			tags:                     map[string]string{"queue": "test", "resource_class": "large"},
			wantErr:                  "",
			wantResources:            largeResourceClass.Resource,
		},
		{
			name:                     "no tag but default set - applies default resource class",
			resourceClasses:          map[string]*config.ResourceClass{"small": smallResourceClass, "large": largeResourceClass},
			defaultResourceClassName: "small",
			tags:                     map[string]string{"queue": "test"},
			wantErr:                  "",
			wantResources:            smallResourceClass.Resource,
		},
		{
			name:                     "tag overrides default - applies tagged resource class",
			resourceClasses:          map[string]*config.ResourceClass{"small": smallResourceClass, "large": largeResourceClass},
			defaultResourceClassName: "small",
			tags:                     map[string]string{"queue": "test", "resource_class": "large"},
			wantErr:                  "",
			wantResources:            largeResourceClass.Resource,
		},
		{
			name:                     "tag specified but resource classes not configured",
			resourceClasses:          nil,
			defaultResourceClassName: "",
			tags:                     map[string]string{"queue": "test", "resource_class": "small"},
			wantErr:                  "resource classes not configured but resource_class tag specified",
			wantResources:            nil,
		},
		{
			name:                     "default set but resource classes not configured",
			resourceClasses:          nil,
			defaultResourceClassName: "small",
			tags:                     map[string]string{"queue": "test"},
			wantErr:                  "resource classes not configured but default-resource-class-name is set",
			wantResources:            nil,
		},
		{
			name:                     "tag references non-existent resource class",
			resourceClasses:          map[string]*config.ResourceClass{"small": smallResourceClass},
			defaultResourceClassName: "",
			tags:                     map[string]string{"queue": "test", "resource_class": "nonexistent"},
			wantErr:                  "resource class not found: nonexistent",
			wantResources:            nil,
		},
		{
			name:                     "default references non-existent resource class",
			resourceClasses:          map[string]*config.ResourceClass{"small": smallResourceClass},
			defaultResourceClassName: "nonexistent",
			tags:                     map[string]string{"queue": "test"},
			wantErr:                  "default resource class not found: nonexistent",
			wantResources:            nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &worker{
				cfg: Config{
					ResourceClasses:          tt.resourceClasses,
					DefaultResourceClassName: tt.defaultResourceClassName,
				},
				logger: logger,
			}

			podSpec := &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "container-0",
						Env:  commandContainerEnv,
					},
				},
			}

			err := w.applyResourceClass(podSpec, tt.tags)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("w.applyResourceClass(podSpec, tt.tags) error = %v, want non-nil error", err)
				}
				if got, want := err.Error(), tt.wantErr; !strings.Contains(got, want) {
					t.Errorf("err.Error() = %q, want containing %q", got, want)
				}
				return
			}

			if err != nil {
				t.Fatalf("w.applyResourceClass(podSpec, tt.tags) error = %v, want nil", err)
			}

			if tt.wantResources == nil {
				if got := len(podSpec.Containers[0].Resources.Requests); got != 0 {
					t.Errorf("len(podSpec.Containers[0].Resources.Requests) = %v, want 0", got)
				}
				if got := len(podSpec.Containers[0].Resources.Limits); got != 0 {
					t.Errorf("len(podSpec.Containers[0].Resources.Limits) = %v, want 0", got)
				}
			} else {
				if diff := cmp.Diff(podSpec.Containers[0].Resources.Requests, tt.wantResources.Requests); diff != "" {
					t.Errorf("podSpec.Containers[0].Resources.Requests diff (-got +want):\n%s", diff)
				}
				if diff := cmp.Diff(podSpec.Containers[0].Resources.Limits, tt.wantResources.Limits); diff != "" {
					t.Errorf("podSpec.Containers[0].Resources.Limits diff (-got +want):\n%s", diff)
				}
			}
		})
	}
}
