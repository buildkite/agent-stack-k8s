package config

import corev1 "k8s.io/api/core/v1"

// CheckoutParams contains parameters that provide additional control over the
// checkout container.
type CheckoutParams struct {
	Skip                 *bool                      `json:"skip,omitempty"`
	CloneFlags           *string                    `json:"cloneFlags,omitempty"`
	FetchFlags           *string                    `json:"fetchFlags,omitempty"`
	GitCredentialsSecret *corev1.SecretVolumeSource `json:"gitCredentialsSecret,omitempty"`
	EnvFrom              []corev1.EnvFromSource     `json:"envFrom,omitempty"`
}

func (co *CheckoutParams) ApplyTo(ctr *corev1.Container) {
	if co == nil || ctr == nil {
		return
	}
	if co.CloneFlags != nil {
		ctr.Env = append(ctr.Env, corev1.EnvVar{
			Name:  "BUILDKITE_GIT_CLONE_FLAGS",
			Value: *co.CloneFlags,
		})
	}
	if co.FetchFlags != nil {
		ctr.Env = append(ctr.Env, corev1.EnvVar{
			Name:  "BUILDKITE_GIT_FETCH_FLAGS",
			Value: *co.FetchFlags,
		})
	}
	ctr.EnvFrom = append(ctr.EnvFrom, co.EnvFrom...)
}

func (co *CheckoutParams) GitCredsSecret() *corev1.SecretVolumeSource {
	if co == nil {
		return nil
	}
	return co.GitCredentialsSecret
}
