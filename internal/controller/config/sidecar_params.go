package config

import corev1 "k8s.io/api/core/v1"

// SidecarParams contains parameters that provide additional control over all sidecar
// container(s).
type SidecarParams struct {
	EnvFrom           []corev1.EnvFromSource `json:"envFrom,omitempty"`
	ExtraVolumeMounts []corev1.VolumeMount   `json:"extraVolumeMounts,omitempty"`
}

func (sc *SidecarParams) ApplyTo(ctr *corev1.Container) {
	if sc == nil || ctr == nil {
		return
	}
	ctr.EnvFrom = append(ctr.EnvFrom, sc.EnvFrom...)
	ctr.VolumeMounts = append(ctr.VolumeMounts, sc.ExtraVolumeMounts...)
}
