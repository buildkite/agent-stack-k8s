package config

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	CommandContainerName = "container-0"
)

type ResourceClass struct {
	Resource     *corev1.ResourceRequirements `json:"resource,omitempty"`
	NodeSelector map[string]string            `json:"nodeSelector,omitempty"`
}

func (rc *ResourceClass) Apply(podSpec *corev1.PodSpec) {
	if rc == nil || podSpec == nil {
		return
	}

	if len(rc.NodeSelector) > 0 {
		if podSpec.NodeSelector == nil {
			podSpec.NodeSelector = make(map[string]string)
		}
		for key, value := range rc.NodeSelector {
			podSpec.NodeSelector[key] = value
		}
	}

	if rc.Resource != nil {
		for i := range podSpec.Containers {
			container := &podSpec.Containers[i]
			if container.Name == CommandContainerName {
				if container.Resources.Requests == nil {
					container.Resources.Requests = make(corev1.ResourceList)
				}
				if container.Resources.Limits == nil {
					container.Resources.Limits = make(corev1.ResourceList)
				}

				if rc.Resource.Requests != nil {
					for resourceName, quantity := range rc.Resource.Requests {
						container.Resources.Requests[resourceName] = quantity
					}
				}

				if rc.Resource.Limits != nil {
					for resourceName, quantity := range rc.Resource.Limits {
						container.Resources.Limits[resourceName] = quantity
					}
				}
				break
			}
		}
	}
}
