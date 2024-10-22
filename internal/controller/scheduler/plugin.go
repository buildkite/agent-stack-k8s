package scheduler

import (
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	corev1 "k8s.io/api/core/v1"
)

type KubernetesPlugin struct {
	PodSpec           *corev1.PodSpec        `json:"podSpec,omitempty"`
	PodSpecPatch      *corev1.PodSpec        `json:"podSpecPatch,omitempty"`
	GitEnvFrom        []corev1.EnvFromSource `json:"gitEnvFrom,omitempty"`
	Sidecars          []corev1.Container     `json:"sidecars,omitempty"`
	Metadata          Metadata               `json:"metadata,omitempty"`
	ExtraVolumeMounts []corev1.VolumeMount   `json:"extraVolumeMounts,omitempty"`
	CheckoutParams    *config.CheckoutParams `json:"checkout,omitempty"`
	CommandParams     *config.CommandParams  `json:"commandParams,omitempty"`
	SidecarParams     *config.SidecarParams  `json:"sidecarParams,omitempty"`
}

type Metadata struct {
	Annotations map[string]string
	Labels      map[string]string
}

// The helper methods below implement "safe navigation" for config fields
// (like &. in Ruby).

// gitEnvFrom returns nil if the receiver is nil, otherwise it returns the
// GitEnvFrom field.
func (kp *KubernetesPlugin) gitEnvFrom() []corev1.EnvFromSource {
	if kp == nil {
		return nil
	}
	return kp.GitEnvFrom
}

// sidecars returns nil if the receiver is nil, otherwise it returns the
// Sidecars field.
func (kp *KubernetesPlugin) sidecars() []corev1.Container {
	if kp == nil {
		return nil
	}
	return kp.Sidecars
}

// extraVolumeMounts returns an empty Metadata if the receiver is nil, otherwise
// it returns the Metadata field.
func (kp *KubernetesPlugin) metadata() Metadata {
	if kp == nil {
		return Metadata{}
	}
	return kp.Metadata
}

// extraVolumeMounts returns nil if the receiver is nil, otherwise it returns
// the ExtraVolumeMounts field.
func (kp *KubernetesPlugin) extraVolumeMounts() []corev1.VolumeMount {
	if kp == nil {
		return nil
	}
	return kp.ExtraVolumeMounts
}

// checkoutParams returns nil if the receiver is nil, otherwise it returns the
// CheckoutParams field.
func (kp *KubernetesPlugin) checkoutParams() *config.CheckoutParams {
	if kp == nil {
		return nil
	}
	return kp.CheckoutParams
}

// commandParams returns nil if the receiver is nil, otherwise it returns the
// CommandParams field.
func (kp *KubernetesPlugin) commandParams() *config.CommandParams {
	if kp == nil {
		return nil
	}
	return kp.CommandParams
}

// sidecarParams returns nil if the receiver is nil, otherwise it returns the
// SidecarParams field.
func (kp *KubernetesPlugin) sidecarParams() *config.SidecarParams {
	if kp == nil {
		return nil
	}
	return kp.SidecarParams
}
