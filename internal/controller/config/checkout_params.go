package config

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// CheckoutParams contains parameters that provide additional control over the
// checkout container.
type CheckoutParams struct {
	Skip                 *bool                      `json:"skip,omitempty"`
	CleanFlags           *string                    `json:"cleanFlags,omitempty"`
	CloneFlags           *string                    `json:"cloneFlags,omitempty"`
	FetchFlags           *string                    `json:"fetchFlags,omitempty"`
	NoSubmodules         *bool                      `json:"noSubmodules,omitempty"`
	SubmoduleCloneConfig []string                   `json:"submoduleCloneConfig,omitempty"`
	GitMirrors           *GitMirrorsParams          `json:"gitMirrors,omitempty"`
	GitCredentialsSecret *corev1.SecretVolumeSource `json:"gitCredentialsSecret,omitempty"`
	EnvFrom              []corev1.EnvFromSource     `json:"envFrom,omitempty"`
}

func (co *CheckoutParams) ApplyTo(podSpec *corev1.PodSpec, ctr *corev1.Container) {
	if co == nil || podSpec == nil || ctr == nil {
		return
	}
	appendToEnvOpt(ctr, "BUILDKITE_GIT_CLEAN_FLAGS", co.CleanFlags)
	appendToEnvOpt(ctr, "BUILDKITE_GIT_CLONE_FLAGS", co.CloneFlags)
	appendToEnvOpt(ctr, "BUILDKITE_GIT_FETCH_FLAGS", co.FetchFlags)
	appendBoolToEnvOpt(ctr, "BUILDKITE_GIT_SUBMODULES", co.NoSubmodules)
	appendCommaSepToEnv(ctr, "BUILDKITE_GIT_SUBMODULE_CLONE_CONFIG", co.SubmoduleCloneConfig)
	co.GitMirrors.ApplyTo(podSpec, ctr)
	ctr.EnvFrom = append(ctr.EnvFrom, co.EnvFrom...)
}

func (co *CheckoutParams) GitCredsSecret() *corev1.SecretVolumeSource {
	if co == nil {
		return nil
	}
	return co.GitCredentialsSecret
}

// GitMirrorsParams configures git mirrors functions of the agent.
type GitMirrorsParams struct {
	Path        *string        `json:"path,omitempty"`
	Volume      *corev1.Volume `json:"volume,omitempty"`
	CloneFlags  *string        `json:"cloneFlags,omitempty"`
	LockTimeout int            `json:"lockTimeout,omitempty"`
	SkipUpdate  *bool          `json:"skipUpdate,omitempty"`
}

func (gm *GitMirrorsParams) ApplyTo(podSpec *corev1.PodSpec, ctr *corev1.Container) {
	if gm == nil || podSpec == nil || ctr == nil {
		return
	}
	if gm.Volume != nil {
		path := "/buildkite/git-mirrors"
		if gm.Path == nil {
			gm.Path = &path
		}
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
			Name:      gm.Volume.Name,
			MountPath: *gm.Path,
		})
		podSpec.Volumes = append(podSpec.Volumes, *gm.Volume)
	}
	appendToEnvOpt(ctr, "BUILDKITE_GIT_MIRRORS_PATH", gm.Path)

	appendToEnvOpt(ctr, "BUILDKITE_GIT_CLONE_MIRROR_FLAGS", gm.CloneFlags)
	if gm.LockTimeout > 0 {
		appendToEnv(ctr, "BUILDKITE_GIT_MIRRORS_LOCK_TIMEOUT", strconv.Itoa(gm.LockTimeout))
	}
	appendBoolToEnvOpt(ctr, "BUILDKITE_GIT_MIRRORS_SKIP_UPDATE", gm.SkipUpdate)
}
