package config

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// CheckoutParams contains parameters that provide additional control over the
// checkout container.
type CheckoutParams struct {
	Skip                 *bool                      `json:"skip,omitempty"`
	CheckoutFlags        *string                    `json:"checkoutFlags,omitempty"`
	CleanFlags           *string                    `json:"cleanFlags,omitempty"`
	CloneFlags           *string                    `json:"cloneFlags,omitempty"`
	FetchFlags           *string                    `json:"fetchFlags,omitempty"`
	NoSubmodules         *bool                      `json:"noSubmodules,omitempty"`
	SubmoduleCloneConfig []string                   `json:"submoduleCloneConfig,omitempty"`
	GitMirrors           *GitMirrorsParams          `json:"gitMirrors,omitempty"`
	GitCredentialsSecret *corev1.SecretVolumeSource `json:"gitCredentialsSecret,omitempty"`
	EnvFrom              []corev1.EnvFromSource     `json:"envFrom,omitempty"`
	ExtraVolumeMounts    []corev1.VolumeMount       `json:"extraVolumeMounts,omitempty"`
}

// ApplyToAgentStart send checkout params's env variables to Agent container
// Agent container will propogate these env variables to command and other containers when they do
// `kubernetes-bootstrap`.
// NOTE:
// It's worthnoting that only some checkout params get passed in this way, many other params are still applied directly
// to checkout container.
// Basically any k8s construct needs to be passed directly to checkout container
func (co *CheckoutParams) ApplyToAgentStart(ctr *corev1.Container) {
	if co == nil || ctr == nil {
		return
	}
	setEnvOpt(ctr, "BUILDKITE_GIT_CHECKOUT_FLAGS", co.CheckoutFlags)
	setEnvOpt(ctr, "BUILDKITE_GIT_CLEAN_FLAGS", co.CleanFlags)
	setEnvOpt(ctr, "BUILDKITE_GIT_CLONE_FLAGS", co.CloneFlags)
	setEnvOpt(ctr, "BUILDKITE_GIT_FETCH_FLAGS", co.FetchFlags)
	setEnvBoolOpt(ctr, "BUILDKITE_NO_GIT_SUBMODULES", co.NoSubmodules)
	// TODO: Agent start doesn't know about submodule clone config, but
	// agent bootstrap does...
	setEnvCommaSep(ctr, "BUILDKITE_GIT_SUBMODULE_CLONE_CONFIG", co.SubmoduleCloneConfig)

	co.GitMirrors.ApplyToAgentStart(ctr)
}

// Any k8s related config things need to be passed to checkout container directly.
// "kubernetes-bootstrap" won't work for those for obvious reason: they are passed k8s pod lifecycle.
//
// NOTE: despite this is called ApplyToCheckout, it mutate not only the container spec but also the pod spec.
func (co *CheckoutParams) ApplyToCheckout(podSpec *corev1.PodSpec, ctr *corev1.Container) {
	if co == nil || podSpec == nil || ctr == nil {
		return
	}
	co.GitMirrors.ApplyToPod(podSpec)
	co.GitMirrors.ApplyToCheckout(ctr)
	ctr.EnvFrom = append(ctr.EnvFrom, co.EnvFrom...)
	ctr.VolumeMounts = append(ctr.VolumeMounts, co.ExtraVolumeMounts...)
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

func (gm *GitMirrorsParams) ApplyToAgentStart(ctr *corev1.Container) {
	if gm == nil || ctr == nil {
		return
	}
	if gm.Volume != nil {
		path := "/buildkite/git-mirrors"
		if gm.Path == nil {
			gm.Path = &path
		}
	}
	setEnvOpt(ctr, "BUILDKITE_GIT_MIRRORS_PATH", gm.Path)
	setEnvOpt(ctr, "BUILDKITE_GIT_CLONE_MIRROR_FLAGS", gm.CloneFlags)
	if gm.LockTimeout > 0 {
		setEnv(ctr, "BUILDKITE_GIT_MIRRORS_LOCK_TIMEOUT", strconv.Itoa(gm.LockTimeout))
	}
	setEnvBoolOpt(ctr, "BUILDKITE_GIT_MIRRORS_SKIP_UPDATE", gm.SkipUpdate)
}

func (gm *GitMirrorsParams) ApplyToCheckout(ctr *corev1.Container) {
	if gm == nil || gm.Volume == nil {
		return
	}
	path := "/buildkite/git-mirrors"
	if gm.Path == nil {
		gm.Path = &path
	}
	ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
		Name:      gm.Volume.Name,
		MountPath: *gm.Path,
	})
}

func (gm *GitMirrorsParams) ApplyToPod(podSpec *corev1.PodSpec) {
	if gm == nil || gm.Volume == nil {
		return
	}
	podSpec.Volumes = append(podSpec.Volumes, *gm.Volume)
}
