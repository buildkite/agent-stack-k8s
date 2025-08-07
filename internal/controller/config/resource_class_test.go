package config

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestResourceClass_Apply(t *testing.T) {
	tests := []struct {
		name          string
		resourceClass *ResourceClass
		podSpec       *corev1.PodSpec
		expected      *corev1.PodSpec
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
						Name:      CommandContainerName,
						Resources: corev1.ResourceRequirements{},
					},
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: CommandContainerName,
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
					{Name: CommandContainerName},
				},
			},
			expected: &corev1.PodSpec{
				NodeSelector: map[string]string{
					"instance-type": "large",
					"zone":          "us-west-2a",
				},
				Containers: []corev1.Container{
					{Name: CommandContainerName},
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
					{Name: CommandContainerName},
				},
			},
			expected: &corev1.PodSpec{
				NodeSelector: map[string]string{
					"instance-type": "xlarge",
				},
				Containers: []corev1.Container{
					{
						Name: CommandContainerName,
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
			name:          "handles nil resource class",
			resourceClass: nil,
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: CommandContainerName},
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: CommandContainerName},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.resourceClass.Apply(tt.podSpec)

			// Compare node selector
			if len(tt.expected.NodeSelector) != len(tt.podSpec.NodeSelector) {
				t.Errorf("NodeSelector length mismatch: expected %d, got %d", len(tt.expected.NodeSelector), len(tt.podSpec.NodeSelector))
			}
			for k, v := range tt.expected.NodeSelector {
				if tt.podSpec.NodeSelector[k] != v {
					t.Errorf("NodeSelector[%s] = %s, expected %s", k, tt.podSpec.NodeSelector[k], v)
				}
			}

			// Compare container resources
			if len(tt.expected.Containers) != len(tt.podSpec.Containers) {
				t.Errorf("Container count mismatch: expected %d, got %d", len(tt.expected.Containers), len(tt.podSpec.Containers))
				return
			}

			for i, expectedContainer := range tt.expected.Containers {
				actualContainer := tt.podSpec.Containers[i]
				if expectedContainer.Name != actualContainer.Name {
					t.Errorf("Container[%d].Name = %s, expected %s", i, actualContainer.Name, expectedContainer.Name)
				}

				// Compare resource requests
				for resourceName, expectedQuantity := range expectedContainer.Resources.Requests {
					actualQuantity := actualContainer.Resources.Requests[resourceName]
					if !actualQuantity.Equal(expectedQuantity) {
						t.Errorf("Container[%d].Resources.Requests[%s] = %s, expected %s", i, resourceName, actualQuantity.String(), expectedQuantity.String())
					}
				}

				// Compare resource limits
				for resourceName, expectedQuantity := range expectedContainer.Resources.Limits {
					actualQuantity := actualContainer.Resources.Limits[resourceName]
					if !actualQuantity.Equal(expectedQuantity) {
						t.Errorf("Container[%d].Resources.Limits[%s] = %s, expected %s", i, resourceName, actualQuantity.String(), expectedQuantity.String())
					}
				}
			}
		})
	}
}
