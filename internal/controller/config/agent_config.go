package config

import (
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	agentcore "github.com/buildkite/agent/v3/core"
)

// AgentConfig stores shared parameters for things that run buildkite-agent in
// one form or another. They should correspond to the flags for
// `buildkite-agent start`. Note that not all agent flags make sense as config
// options for agents running within a pod.
type AgentConfig struct {
	// Applies to agents within the k8s controller and within the pod
	Endpoint *string `json:"endpoint,omitempty"` // BUILDKITE_AGENT_ENDPOINT
	NoHTTP2  bool    `json:"no-http2,omitempty"` // BUILDKITE_NO_HTTP2

	// Only applies to agents within the pod
	Experiments               []string `json:"experiment,omitempty"`                   // BUILDKITE_AGENT_EXPERIMENT
	Shell                     *string  `json:"shell,omitempty"`                        // BUILDKITE_SHELL
	NoColor                   bool     `json:"no-color,omitempty"`                     // BUILDKITE_AGENT_NO_COLOR
	StrictSingleHooks         bool     `json:"strict-single-hooks,omitempty"`          // BUILDKITE_STRICT_SINGLE_HOOKS
	NoMultipartArtifactUpload bool     `json:"no-multipart-artifact-upload,omitempty"` // BUILDKITE_NO_MULTIPART_ARTIFACT_UPLOAD
	TraceContextEncoding      *string  `json:"trace-context-encoding,omitempty"`       // BUILDKITE_TRACE_CONTEXT_ENCODING
	DisableWarningsFor        []string `json:"disable-warnings-for,omitempty"`         // BUILDKITE_AGENT_DISABLE_WARNINGS_FOR
	DebugSigning              bool     `json:"debug-signing,omitempty"`                // BUILDKITE_AGENT_DEBUG_SIGNING

	// Applies differently depending on the container
	//                                                         // agent start                    / bootstrap
	NoPTY            bool `json:"no-pty,omitempty"`            // BUILDKITE_NO_PTY               / BUILDKITE_PTY
	NoCommandEval    bool `json:"no-command-eval,omitempty"`   // BUILDKITE_NO_COMMAND_EVAL      / BUILDKITE_COMMAND_EVAL
	NoLocalHooks     bool `json:"no-local-hooks,omitempty"`    // BUILDKITE_NO_LOCAL_HOOKS       / BUILDKITE_LOCAL_HOOKS_ENABLED
	NoPlugins        bool `json:"no-plugins,omitempty"`        // BUILDKITE_NO_PLUGINS           / BUILDKITE_PLUGINS_ENABLED
	PluginValidation bool `json:"plugin-validation,omitempty"` // BUILDKITE_NO_PLUGIN_VALIDATION / BUILDKITE_PLUGIN_VALIDATION

	// Like the above, but signing keys can be supplied directly to the command container.
	//                                                                      // agent start                         / pipeline upload or agent tool sign
	SigningJWKSFile   *string        `json:"signing-jwks-file,omitempty"`   // BUILDKITE_AGENT_SIGNING_JWKS_FILE   / BUILDKITE_AGENT_JWKS_FILE
	SigningJWKSKeyID  *string        `json:"signing-jwks-key-id,omitempty"` // BUILDKITE_AGENT_SIGNING_JWKS_KEY_ID / BUILDKITE_AGENT_JWKS_KEY_ID
	SigningJWKSVolume *corev1.Volume `json:"signingJWKSVolume,omitempty"`

	// Hooks and plugins can be supplied with a volume source.
	HooksPath     *string        `json:"hooks-path,omitempty"` // BUILDKITE_HOOKS_PATH
	HooksVolume   *corev1.Volume `json:"hooksVolume,omitempty"`
	PluginsPath   *string        `json:"plugins-path,omitempty"` // BUILDKITE_PLUGINS_PATH
	PluginsVolume *corev1.Volume `json:"pluginsVolume,omitempty"`

	// Applies only to the "buildkite-agent start" container.
	// Keys can be supplied with a volume.
	VerificationJWKSFile        *string        `json:"verification-jwks-file,omitempty"`        // BUILDKITE_AGENT_VERIFICATION_JWKS_FILE
	VerificationFailureBehavior *string        `json:"verification-failure-behavior,omitempty"` // BUILDKITE_AGENT_JOB_VERIFICATION_NO_SIGNATURE_BEHAVIOR
	VerificationJWKSVolume      *corev1.Volume `json:"verificationJWKSVolume,omitempty"`
}

func (a *AgentConfig) ControllerOptions() []agentcore.ControllerOption {
	if a == nil {
		return nil
	}
	var opts []agentcore.ControllerOption
	if a.Endpoint != nil {
		opts = append(opts, agentcore.WithEndpoint(*a.Endpoint))
	}
	if a.NoHTTP2 {
		opts = append(opts, agentcore.WithAllowHTTP2(false))
	}
	return opts
}

// ApplyVolumesTo adds volumes based on the agent config to the podSpec.
func (a *AgentConfig) ApplyVolumesTo(podSpec *corev1.PodSpec) {
	if a == nil || podSpec == nil {
		return
	}
	if a.HooksVolume != nil {
		podSpec.Volumes = append(podSpec.Volumes, *a.HooksVolume)
	}
	if a.PluginsVolume != nil {
		podSpec.Volumes = append(podSpec.Volumes, *a.PluginsVolume)
	}
	if a.SigningJWKSVolume != nil {
		podSpec.Volumes = append(podSpec.Volumes, *a.SigningJWKSVolume)
	}
	if a.VerificationJWKSVolume != nil {
		podSpec.Volumes = append(podSpec.Volumes, *a.VerificationJWKSVolume)
	}
}

// applyCommonTo applies env vars and volume mounts that are the same among all
// containers that run buildkite-agent in some form.
func (a *AgentConfig) applyCommonTo(ctr *corev1.Container) {
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_ENDPOINT", a.Endpoint)
	appendBoolToEnv(ctr, "BUILDKITE_NO_HTTP2", a.NoHTTP2)
	appendCommaSepToEnv(ctr, "BUILDKITE_AGENT_EXPERIMENT", a.Experiments)
	appendToEnvOpt(ctr, "BUILDKITE_SHELL", a.Shell)
	appendBoolToEnv(ctr, "BUILDKITE_AGENT_NO_COLOR", a.NoColor)
	appendBoolToEnv(ctr, "BUILDKITE_STRICT_SINGLE_HOOKS", a.StrictSingleHooks)
	appendBoolToEnv(ctr, "BUILDKITE_NO_MULTIPART_ARTIFACT_UPLOAD", a.NoMultipartArtifactUpload)
	appendToEnvOpt(ctr, "BUILDKITE_TRACE_CONTEXT_ENCODING", a.TraceContextEncoding)
	appendCommaSepToEnv(ctr, "BUILDKITE_AGENT_DISABLE_WARNINGS_FOR", a.DisableWarningsFor)
	appendBoolToEnv(ctr, "BUILDKITE_AGENT_DEBUG_SIGNING", a.DebugSigning)

	if a.HooksVolume != nil {
		hooksPath := "/buildkite/hooks"
		if a.HooksPath == nil {
			a.HooksPath = &hooksPath
		}
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
			Name:      a.HooksVolume.Name,
			MountPath: *a.HooksPath,
		})
	}
	appendToEnvOpt(ctr, "BUILDKITE_HOOKS_PATH", a.HooksPath)

	if a.PluginsVolume != nil {
		pluginsPath := "/buildkite/plugins"
		if a.PluginsPath == nil {
			a.PluginsPath = &pluginsPath
		}
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
			Name:      a.HooksVolume.Name,
			MountPath: *a.PluginsPath,
		})
	}
	appendToEnvOpt(ctr, "BUILDKITE_PLUGINS_PATH", a.PluginsPath)
}

// ApplyToAgentStart adds env vars assuming ctr is the agent "server" container.
func (a *AgentConfig) ApplyToAgentStart(ctr *corev1.Container) {
	if a == nil || ctr == nil {
		return
	}
	a.applyCommonTo(ctr)

	appendBoolToEnv(ctr, "BUILDKITE_NO_PTY", a.NoPTY)
	appendBoolToEnv(ctr, "BUILDKITE_NO_COMMAND_EVAL", a.NoCommandEval)
	appendBoolToEnv(ctr, "BUILDKITE_NO_LOCAL_HOOKS", a.NoLocalHooks)
	appendBoolToEnv(ctr, "BUILDKITE_NO_PLUGINS", a.NoPlugins)
	appendBoolToEnv(ctr, "BUILDKITE_NO_PLUGIN_VALIDATION", !a.PluginValidation)

	if a.VerificationJWKSVolume != nil {
		dir, file := "/buildkite/verification-jwks", "key"
		if a.VerificationJWKSFile == nil {
			a.VerificationJWKSFile = &file
		}
		if filepath.IsAbs(*a.VerificationJWKSFile) {
			dir = filepath.Dir(*a.VerificationJWKSFile)
		} else {
			*a.VerificationJWKSFile = filepath.Join(dir, *a.VerificationJWKSFile)
		}
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
			Name:      a.VerificationJWKSVolume.Name,
			MountPath: dir,
		})
	}
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_VERIFICATION_JWKS_FILE", a.VerificationJWKSFile)

	if a.VerificationJWKSFile == nil && a.VerificationFailureBehavior == nil {
		// The agent defaults to "block", but this makes it slightly harder to
		// incremenetally adopt signed pipelines because signatures can be added
		// from differently configured agents, but "block" mode means the agent
		// rejects jobs with secrets if the key is missing.
		a.VerificationFailureBehavior = ptr.To("warn")
	}
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_JOB_VERIFICATION_NO_SIGNATURE_BEHAVIOR", a.VerificationFailureBehavior)
}

// applyToBootstrap adds env vars assuming ctr is a checkout or command container.
func (a *AgentConfig) applyToBootstrap(ctr *corev1.Container) {
	a.applyCommonTo(ctr)
	// Note that these "buildkite-agent start"-like options are applied to
	// containers running "buildkite-agent bootstrap". So e.g. noPTY:true must
	// be inverted to pty:false, as the agent would normally.
	appendBoolToEnv(ctr, "BUILDKITE_PTY", !a.NoPTY)
	appendBoolToEnv(ctr, "BUILDKITE_COMMAND_EVAL", !a.NoCommandEval)
	appendBoolToEnv(ctr, "BUILDKITE_LOCAL_HOOKS_ENABLED", !a.NoLocalHooks)
	appendBoolToEnv(ctr, "BUILDKITE_PLUGINS_ENABLED", !a.NoPlugins)
	appendBoolToEnv(ctr, "BUILDKITE_PLUGIN_VALIDATION", a.PluginValidation)
}

// ApplyToCheckout adds env vars assuming ctr is a checkout container.
func (a *AgentConfig) ApplyToCheckout(ctr *corev1.Container) {
	if a == nil || ctr == nil {
		return
	}
	a.applyToBootstrap(ctr)
}

// ApplyToCommand adds env vars assuming ctr is a command container.
func (a *AgentConfig) ApplyToCommand(ctr *corev1.Container) {
	if a == nil || ctr == nil {
		return
	}
	a.applyToBootstrap(ctr)
	// Signing happens either with "pipeline upload" or "tool sign", so the
	// signing side of any key needs to be attached to the command container,
	// not the agent start container or checkout containers.
	//
	// If there is a volume source for a key, then allow a key file to be nil,
	// an absolute path, or a relative path.
	// If the key file is nil, use a default directory and file name.
	// If the key file is relative, use a default directory and treat the file
	// as relative to that directory.
	// If the key file is absolute, use its directory as the mount path for the
	// volume.
	// If there is no volume source for a key, it's up to the user whether they
	// use signing with an absolute path, or not use signing (nil).
	if a.SigningJWKSVolume != nil {
		dir, file := "/buildkite/signing-jwks", "key"
		if a.SigningJWKSFile == nil {
			a.SigningJWKSFile = &file
		}
		if filepath.IsAbs(*a.SigningJWKSFile) {
			dir = filepath.Dir(*a.SigningJWKSFile)
		} else {
			*a.SigningJWKSFile = filepath.Join(dir, *a.SigningJWKSFile)
		}
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
			Name:      a.SigningJWKSVolume.Name,
			MountPath: dir,
		})
	}
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_JWKS_FILE", a.SigningJWKSFile)
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_JWKS_KEY_ID", a.SigningJWKSKeyID)
}
