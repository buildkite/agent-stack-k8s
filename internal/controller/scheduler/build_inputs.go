package scheduler

import v1 "k8s.io/api/core/v1"

func applyCustomImageIfPresent(podSpec *v1.PodSpec, inputs *buildInputs) *v1.PodSpec {

	customImage := inputs.envMap["BUILDKITE_IMAGE"]
	if customImage == "" {
		return podSpec
	}

	for i := range podSpec.Containers {
		c := &podSpec.Containers[i]
		if c.Name != CommandContainerName {
			continue
		}

		c.Image = customImage
	}

	return podSpec
}
