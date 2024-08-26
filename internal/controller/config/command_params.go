package config

import corev1 "k8s.io/api/core/v1"

// CommandParams contains parameters that provide additional control over all
// command container(s).
type CommandParams struct {
	EnvFrom    []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

func (cmd *CommandParams) ApplyTo(ctr *corev1.Container) {
	if cmd == nil || ctr == nil {
		return
	}
	ctr.EnvFrom = append(ctr.EnvFrom, cmd.EnvFrom...)
}
