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
	NoHTTP2  *bool   `json:"no-http2,omitempty"` // BUILDKITE_NO_HTTP2

	// Only applies to agents within the pod
	Experiments               []string `json:"experiment,omitempty"`                   // BUILDKITE_AGENT_EXPERIMENT
	Shell                     *string  `json:"shell,omitempty"`                        // BUILDKITE_SHELL
	NoColor                   *bool    `json:"no-color,omitempty"`                     // BUILDKITE_AGENT_NO_COLOR
	StrictSingleHooks         *bool    `json:"strict-single-hooks,omitempty"`          // BUILDKITE_STRICT_SINGLE_HOOKS
	NoMultipartArtifactUpload *bool    `json:"no-multipart-artifact-upload,omitempty"` // BUILDKITE_NO_MULTIPART_ARTIFACT_UPLOAD
	TraceContextEncoding      *string  `json:"trace-context-encoding,omitempty"`       // BUILDKITE_TRACE_CONTEXT_ENCODING
	DisableWarningsFor        []string `json:"disable-warnings-for,omitempty"`         // BUILDKITE_AGENT_DISABLE_WARNINGS_FOR
	DebugSigning              *bool    `json:"debug-signing,omitempty"`                // BUILDKITE_AGENT_DEBUG_SIGNING

	// Applies differently depending on the container
	//                                                          // agent start                    / bootstrap
	NoPTY            *bool `json:"no-pty,omitempty"`            // BUILDKITE_NO_PTY               / BUILDKITE_PTY
	NoCommandEval    *bool `json:"no-command-eval,omitempty"`   // BUILDKITE_NO_COMMAND_EVAL      / BUILDKITE_COMMAND_EVAL
	NoLocalHooks     *bool `json:"no-local-hooks,omitempty"`    // BUILDKITE_NO_LOCAL_HOOKS       / BUILDKITE_LOCAL_HOOKS_ENABLED
	NoPlugins        *bool `json:"no-plugins,omitempty"`        // BUILDKITE_NO_PLUGINS           / BUILDKITE_PLUGINS_ENABLED
	PluginValidation *bool `json:"plugin-validation,omitempty"` // BUILDKITE_NO_PLUGIN_VALIDATION / BUILDKITE_PLUGIN_VALIDATION

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
	if a.NoHTTP2 != nil {
		opts = append(opts, agentcore.WithAllowHTTP2(*a.NoHTTP2))
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

// ApplyToAgentStart adds env vars assuming ctr is the agent "server" container.
func (a *AgentConfig) ApplyToAgentStart(ctr *corev1.Container) {
	if a == nil || ctr == nil {
		return
	}
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_ENDPOINT", a.Endpoint)
	appendBoolToEnvOpt(ctr, "BUILDKITE_NO_HTTP2", a.NoHTTP2)
	appendCommaSepToEnv(ctr, "BUILDKITE_AGENT_EXPERIMENT", a.Experiments)
	appendToEnvOpt(ctr, "BUILDKITE_SHELL", a.Shell)
	appendBoolToEnvOpt(ctr, "BUILDKITE_AGENT_NO_COLOR", a.NoColor)
	appendBoolToEnvOpt(ctr, "BUILDKITE_STRICT_SINGLE_HOOKS", a.StrictSingleHooks)
	appendBoolToEnvOpt(ctr, "BUILDKITE_NO_MULTIPART_ARTIFACT_UPLOAD", a.NoMultipartArtifactUpload)
	appendToEnvOpt(ctr, "BUILDKITE_TRACE_CONTEXT_ENCODING", a.TraceContextEncoding)
	appendCommaSepToEnv(ctr, "BUILDKITE_AGENT_DISABLE_WARNINGS_FOR", a.DisableWarningsFor)
	appendBoolToEnvOpt(ctr, "BUILDKITE_AGENT_DEBUG_SIGNING", a.DebugSigning)

	a.applyHooksVolumeTo(ctr)
	setEnvVarIfAbsentOrDifferent(ctr, "BUILDKITE_HOOKS_PATH", a.HooksPath)

	a.applyPluginsVolumeTo(ctr)
	appendToEnvOpt(ctr, "BUILDKITE_PLUGINS_PATH", a.PluginsPath)

	// The agent transforms these into the corresponding negated versions when
	// passing env vars down to the bootstrap.
	appendBoolToEnvOpt(ctr, "BUILDKITE_NO_PTY", a.NoPTY)
	appendBoolToEnvOpt(ctr, "BUILDKITE_NO_COMMAND_EVAL", a.NoCommandEval)
	appendBoolToEnvOpt(ctr, "BUILDKITE_NO_LOCAL_HOOKS", a.NoLocalHooks)
	appendBoolToEnvOpt(ctr, "BUILDKITE_NO_PLUGINS", a.NoPlugins)
	appendNegatedToEnvOpt(ctr, "BUILDKITE_NO_PLUGIN_VALIDATION", a.PluginValidation)

	// The signing key, if provided to the agent, is transformed by the agent
	// into the corresponding env var for subprocesses.
	// But the file itself is used by volume should be attached to the command container!
	// If there is no SigningJWKSVolume but the user set SigningJWKSFile, it's
	// up to the user to set it to the path of a key file supplied in their
	// container.
	if a.SigningJWKSFile != nil {
		normaliseJWKSFile(a.SigningJWKSVolume, &a.SigningJWKSFile, "/buildkite/signing-jwks", "key")
	}
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_SIGNING_JWKS_FILE", a.SigningJWKSFile)
	appendToEnvOpt(ctr, "BUILDKITE_AGENT_SIGNING_JWKS_KEY_ID", a.SigningJWKSKeyID)

	// See notes in normaliseJWKSFile about the default directory/file handling.
	// Similarly to SigningJWKSFile, if there is no VerificationJWKSVolume but
	// the user set VerificationJWKSFile, it's up to the user to set it to the
	// path of a key file supplied in their container.
	if a.VerificationJWKSVolume != nil {
		dir := normaliseJWKSFile(a.VerificationJWKSVolume, &a.VerificationJWKSFile, "/buildkite/verification-jwks", "key")
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

func (a *AgentConfig) ApplyToCommand(ctr *corev1.Container) {
	if a == nil || ctr == nil {
		return
	}
	a.applyHooksVolumeTo(ctr)
	a.applyPluginsVolumeTo(ctr)
	// Signing happens either with "pipeline upload" or "tool sign", so the
	// signing side of any key needs to be attached to the command container,
	// not the agent start container or checkout containers.
	if a.SigningJWKSVolume == nil {
		return
	}
	dir := normaliseJWKSFile(a.SigningJWKSVolume, &a.SigningJWKSFile, "/buildkite/signing-jwks", "key")
	ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
		Name:      a.SigningJWKSVolume.Name,
		MountPath: dir,
	})
}

func (a *AgentConfig) ApplyToCheckout(ctr *corev1.Container) {
	if a == nil || ctr == nil {
		return
	}
	a.applyHooksVolumeTo(ctr)
	a.applyPluginsVolumeTo(ctr)
}

func (a *AgentConfig) applyHooksVolumeTo(ctr *corev1.Container) {
	if a == nil || ctr == nil || a.HooksVolume == nil {
		return
	}
	if a.HooksPath == nil {
		a.HooksPath = ptr.To("/workspace/hooks")
	}
	ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
		Name:      a.HooksVolume.Name,
		MountPath: *a.HooksPath,
	})
}

func (a *AgentConfig) applyPluginsVolumeTo(ctr *corev1.Container) {
	if a == nil || ctr == nil || a.PluginsVolume == nil {
		return
	}
	if a.PluginsPath == nil {
		a.PluginsPath = ptr.To("/workspace/plugins")
	}
	ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{
		Name:      a.PluginsVolume.Name,
		MountPath: *a.PluginsPath,
	})
}

func setEnvVarIfAbsentOrDifferent(ctr *corev1.Container, name string, value *string) {
	if ctr == nil || value == nil {
		return
	}
	for i := range ctr.Env {
		if ctr.Env[i].Name == name {
			if ctr.Env[i].Value != *value {
				ctr.Env[i].Value = *value
			}
			return
		}
	}
	ctr.Env = append(ctr.Env, corev1.EnvVar{Name: name, Value: *value})
}

// normaliseJWKSFile normalises the *string field pointed to by jwksFileField
// (so either AgentConfig.SigningJWKSFile or AgentConfig.VerificationJWKSFile)
// to a default depending on what already exists and the volume being used.
// This allows the field to be nil, or contain an absolute or relative path.
// normaliseJWKSFile is idempotent.
// It returns the directory (either the default or taken from the supplied
// jwksFileField) for use in the VolumeMount.
//
// If the jwksFileField points to nil, normalise it point to a string with a
// default directory and file name.
// If the field points to a string containing a relative path, normalise it
// to use a default directory, treating the original path as relative to that
// default directory.
// If the field points to a string containing an absolute path, return its
// directory.
func normaliseJWKSFile(volume *corev1.Volume, jwksFileField **string, defaultDir, defaultFile string) string {
	if *jwksFileField == nil {
		*jwksFileField = ptr.To(defaultFile)
	}
	if filepath.IsAbs(**jwksFileField) {
		return filepath.Dir(**jwksFileField)
	}
	**jwksFileField = filepath.Join(defaultDir, **jwksFileField)
	return defaultDir
}
