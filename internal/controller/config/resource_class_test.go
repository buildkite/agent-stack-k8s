package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestResourceClass_Apply(t *testing.T) {
	commandContainerEnv := []corev1.EnvVar{
		{
			Name:  "BUILDKITE_BOOTSTRAP_PHASES",
			Value: "plugin,command",
		},
		{
			Name:  "BUILDKITE_COMMAND",
			Value: "buildkite-agent foo",
		},
	}
	tests := []struct {
		name          string
		resourceClass *ResourceClass
		podSpec       *corev1.PodSpec
		want          *corev1.PodSpec
	}{
		{
			name: "applies resource requirements to command container",
			resourceClass: &ResourceClass{
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
			},
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{},
						Env:       commandContainerEnv,
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
			},
		},
		{
			name: "applies node selector",
			resourceClass: &ResourceClass{
				NodeSelector: map[string]string{
					"instance-type": "large",
					"zone":          "us-west-2a",
				},
			},
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
					},
				},
			},
			want: &corev1.PodSpec{
				NodeSelector: map[string]string{
					"instance-type": "large",
					"zone":          "us-west-2a",
				},
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
					},
				},
			},
		},
		{
			name: "applies both resource requirements and node selector",
			resourceClass: &ResourceClass{
				Resource: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("500m"),
					},
				},
				NodeSelector: map[string]string{
					"instance-type": "xlarge",
				},
			},
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
					},
				},
			},
			want: &corev1.PodSpec{
				NodeSelector: map[string]string{
					"instance-type": "xlarge",
				},
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("500m"),
							},
							Limits: corev1.ResourceList{},
						},
					},
				},
			},
		},
		{
			name: "applies resource requirements when BUILDKITE_COMMAND is empty",
			resourceClass: &ResourceClass{
				Resource: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("100m"),
					},
				},
			},
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: []corev1.EnvVar{
							{
								Name:  "BUILDKITE_BOOTSTRAP_PHASES",
								Value: "plugin,command",
							},
							{
								Name:  "BUILDKITE_COMMAND",
								Value: "",
							},
						},
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: []corev1.EnvVar{
							{
								Name:  "BUILDKITE_BOOTSTRAP_PHASES",
								Value: "plugin,command",
							},
							{
								Name:  "BUILDKITE_COMMAND",
								Value: "",
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("100m"),
							},
							Limits: corev1.ResourceList{},
						},
					},
				},
			},
		},
		{
			name:          "handles nil resource class",
			resourceClass: nil,
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
					},
				},
			},
			want: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: commandContainerEnv,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.resourceClass.Apply(tt.podSpec)

			// Compare the entire PodSpec using cmp.Diff
			if diff := cmp.Diff(tt.want, tt.podSpec); diff != "" {
				t.Errorf("PodSpec mismatch (-expected +actual):\n%s", diff)
			}
		})
	}
}
