package config

import (
	"maps"

	corev1 "k8s.io/api/core/v1"
)

const (
	// This is duplicated from scheduler to avoid cyclic dependencies, it might be a good time to introduce a constant
	// module now
	commandContainerName = "container-0"
)

// ResourceClass represents a reusable resource configuration.
// Affinity or Toleration/taint based configuration may come later.
type ResourceClass struct {
	Resource     *corev1.ResourceRequirements `json:"resource,omitempty"`
	NodeSelector map[string]string            `json:"nodeSelector,omitempty"`
}

// Apply adds the resource class NodeSelector to the podSpec, and resource
// requests and limits to the command container. It assumes there is only one
// command container named "container-0".
func (rc *ResourceClass) Apply(podSpec *corev1.PodSpec) {
	if rc == nil || podSpec == nil {
		return
	}

	if len(rc.NodeSelector) > 0 {
		if podSpec.NodeSelector == nil {
			podSpec.NodeSelector = make(map[string]string)
		}
		maps.Copy(podSpec.NodeSelector, rc.NodeSelector)
	}

	if rc.Resource != nil {
		for i := range podSpec.Containers {
			container := &podSpec.Containers[i]

			// We only care about command container.
			// checkout and other containers resources can be configured via controller setting.
			if container.Name != commandContainerName {
				continue
			}

			if container.Resources.Requests == nil {
				container.Resources.Requests = make(corev1.ResourceList)
			}
			if container.Resources.Limits == nil {
				container.Resources.Limits = make(corev1.ResourceList)
			}

			maps.Copy(container.Resources.Requests, rc.Resource.Requests)
			maps.Copy(container.Resources.Limits, rc.Resource.Limits)
		}
	}
}
