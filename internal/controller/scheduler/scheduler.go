package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/buildkite/agent/v3/clicommand"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	defaultTermGracePeriodSeconds = 60
	agentTokenKey                 = "BUILDKITE_AGENT_TOKEN"
	AgentContainerName            = "agent"
	CopyAgentContainerName        = "copy-agent"
	CheckoutContainerName         = "checkout"
)

var errK8sPluginProhibited = errors.New("the kubernetes plugin is prohibited by this controller, but was configured on this job")

type Config struct {
	Namespace              string
	Image                  string
	AgentToken             string
	JobTTL                 time.Duration
	AdditionalRedactedVars []string
	DefaultCheckoutParams  *config.CheckoutParams
	DefaultCommandParams   *config.CommandParams
	DefaultSidecarParams   *config.SidecarParams
	PodSpecPatch           *corev1.PodSpec
	ProhibitK8sPlugin      bool
}

func New(logger *zap.Logger, client kubernetes.Interface, cfg Config) *worker {
	return &worker{
		cfg:    cfg,
		client: client,
		logger: logger.Named("worker"),
	}
}

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

type worker struct {
	cfg    Config
	client kubernetes.Interface
	logger *zap.Logger
}

func (w *worker) Create(ctx context.Context, job *api.CommandJob) error {
	logger := w.logger.With(zap.String("uuid", job.Uuid))
	logger.Info("creating job")

	inputs, err := w.ParseJob(job)
	if err != nil {
		logger.Warn("Plugin parsing failed, creating failure job instead", zap.Error(err))
		return w.buildAndCreateFallbackJob(ctx, err, inputs)
	}

	// Default command container using default image.
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Image:   w.cfg.Image,
				Command: []string{job.Command},
			},
		},
	}
	// Use the podSpec provided by the plugin, if allowed.
	if inputs.k8sPlugin != nil && inputs.k8sPlugin.PodSpec != nil {
		podSpec = inputs.k8sPlugin.PodSpec
	}

	kjob, err := w.Build(podSpec, false, inputs)
	if err != nil {
		logger.Warn("Job definition error detected, creating failure job instead", zap.Error(err))
		return w.buildAndCreateFallbackJob(ctx, err, inputs)
	}

	err = w.createJob(ctx, kjob)
	if kerrors.IsInvalid(err) {
		logger.Warn("Job creation failed, creating failure job instead", zap.Error(err))
		return w.buildAndCreateFallbackJob(ctx, err, inputs)
	}
	return err
}

func (w *worker) buildAndCreateFallbackJob(ctx context.Context, err error, inputs buildInputs) error {
	kjob, ferr := w.BuildFailureJob(err, inputs)
	if ferr != nil {
		return fmt.Errorf("job definition error and failure job definition error: %w", err)
	}
	return w.createJob(ctx, kjob)
}

func (w *worker) createJob(ctx context.Context, kjob *batchv1.Job) error {
	_, err := w.client.BatchV1().Jobs(w.cfg.Namespace).Create(ctx, kjob, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}
	return nil
}

// buildInputs contains the relevant components of a CommandJob needed for Build.
type buildInputs struct {
	// Taken from the job directly.
	uuid            string
	command         string
	agentQueryRules []string

	// Involves some parsing of the job env / plugins map
	envMap       map[string]string
	k8sPlugin    *KubernetesPlugin
	otherPlugins []map[string]json.RawMessage
}

func (w *worker) ParseJob(job *api.CommandJob) (buildInputs, error) {
	parsed := buildInputs{
		uuid:            job.Uuid,
		command:         job.Command,
		agentQueryRules: job.AgentQueryRules,
		envMap:          make(map[string]string),
	}

	for _, val := range job.Env {
		parts := strings.SplitN(val, "=", 2)
		parsed.envMap[parts[0]] = parts[1]
	}
	var plugins []map[string]json.RawMessage
	if pluginsJSON, ok := parsed.envMap["BUILDKITE_PLUGINS"]; ok {
		if err := json.Unmarshal([]byte(pluginsJSON), &plugins); err != nil {
			w.logger.Debug("invalid plugin spec", zap.String("json", pluginsJSON))
			return parsed, fmt.Errorf("failed parsing plugins: %w", err)
		}
	}
	w.logger.Info("parsing", zap.Any("plugins", plugins))
	for _, plugin := range plugins {
		if len(plugin) != 1 {
			return parsed, fmt.Errorf("found invalid plugin: %v", plugin)
		}
		val, isK8sPlugin := plugin["github.com/buildkite-plugins/kubernetes-buildkite-plugin"]
		if !isK8sPlugin {
			for k, v := range plugin {
				parsed.otherPlugins = append(parsed.otherPlugins, map[string]json.RawMessage{k: v})
			}
			continue
		}
		// plugin is the k8s plugin. If the plugin is prohibited, fail.
		if w.cfg.ProhibitK8sPlugin {
			return parsed, errK8sPluginProhibited
		}
		if err := json.Unmarshal(val, &parsed.k8sPlugin); err != nil {
			return parsed, fmt.Errorf("failed parsing Kubernetes plugin: %w", err)
		}
	}
	return parsed, nil
}

// Build builds a job. The checkout container will be skipped either by passing
// `true` or if the configuration is configured to skip it.
func (w *worker) Build(podSpec *corev1.PodSpec, skipCheckout bool, inputs buildInputs) (*batchv1.Job, error) {
	// If Build was called with skipCheckout == false, then look at the config
	// and plugin.
	if !skipCheckout {
		// Start with the default, if set
		if co := w.cfg.DefaultCheckoutParams; co != nil && co.Skip != nil {
			skipCheckout = *co.Skip
		}
		// The plugin overrides the default, if set
		if inputs.k8sPlugin != nil {
			if co := inputs.k8sPlugin.CheckoutParams; co != nil && co.Skip != nil {
				skipCheckout = *co.Skip
			}
		}
	}

	kjob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        kjobName(inputs.uuid),
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}

	if inputs.k8sPlugin != nil {
		maps.Copy(kjob.Labels, inputs.k8sPlugin.Metadata.Labels)
		maps.Copy(kjob.Annotations, inputs.k8sPlugin.Metadata.Annotations)
	}

	kjob.Labels[config.UUIDLabel] = inputs.uuid
	w.labelWithAgentTags(kjob.Labels, inputs.agentQueryRules)
	kjob.Annotations[config.BuildURLAnnotation] = inputs.envMap["BUILDKITE_BUILD_URL"]
	w.annotateWithJobURL(kjob.Annotations, inputs.uuid, inputs.envMap)

	// Prevent k8s cluster autoscaler from terminating the job before it finishes to scale down cluster
	kjob.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"] = "false"

	kjob.Spec.Template.Labels = kjob.Labels
	kjob.Spec.Template.Annotations = kjob.Annotations
	kjob.Spec.BackoffLimit = ptr.To[int32](0)
	kjob.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To[int64](defaultTermGracePeriodSeconds)

	env := []corev1.EnvVar{
		{
			Name:  "BUILDKITE_BUILD_PATH",
			Value: "/workspace/build",
		},
		{
			Name:  "BUILDKITE_BIN_PATH",
			Value: "/workspace",
		},
		{
			Name: agentTokenKey,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: w.cfg.AgentToken},
					Key:                  agentTokenKey,
				},
			},
		},
		{
			Name:  "BUILDKITE_AGENT_ACQUIRE_JOB",
			Value: inputs.uuid,
		},
	}
	if len(inputs.otherPlugins) > 0 {
		otherPluginsJSON, err := json.Marshal(inputs.otherPlugins)
		if err != nil {
			return nil, fmt.Errorf("failed to remarshal non-k8s plugins: %w", err)
		}
		env = append(env, corev1.EnvVar{
			Name:  "BUILDKITE_PLUGINS",
			Value: string(otherPluginsJSON),
		})
	}
	for k, v := range inputs.envMap {
		switch k {
		case "BUILDKITE_COMMAND", "BUILDKITE_ARTIFACT_PATHS", "BUILDKITE_PLUGINS": // noop
		default:
			env = append(env, corev1.EnvVar{Name: k, Value: v})
		}
	}

	redactedVars := append([]string(nil), clicommand.RedactedVars.Value.Value()...)
	redactedVars = append(redactedVars, w.cfg.AdditionalRedactedVars...)
	env = append(env, corev1.EnvVar{
		Name:  clicommand.RedactedVars.EnvVar,
		Value: strings.Join(redactedVars, ","),
	})

	volumeMounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	if inputs.k8sPlugin != nil {
		volumeMounts = append(volumeMounts, inputs.k8sPlugin.ExtraVolumeMounts...)
	}

	systemContainerCount := 0
	if !skipCheckout {
		systemContainerCount = 1
	}

	ttl := int32(w.cfg.JobTTL.Seconds())
	kjob.Spec.TTLSecondsAfterFinished = &ttl

	containerEnv := append([]corev1.EnvVar{}, env...)
	containerEnv = append(containerEnv, []corev1.EnvVar{
		{
			Name:  "BUILDKITE_KUBERNETES_EXEC",
			Value: "true",
		},
		{
			Name:  "BUILDKITE_BOOTSTRAP_PHASES",
			Value: "plugin,command",
		},
		{
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		},
		{
			Name:  "BUILDKITE_SHELL",
			Value: "/bin/sh -ec",
		},
		{
			Name:  "BUILDKITE_ARTIFACT_PATHS",
			Value: inputs.envMap["BUILDKITE_ARTIFACT_PATHS"],
		},
		{
			Name:  "BUILDKITE_PLUGINS_PATH",
			Value: "/workspace/plugins",
		},
		{
			Name:  "BUILDKITE_SOCKETS_PATH",
			Value: "/workspace/sockets",
		},
	}...)

	for i, c := range podSpec.Containers {
		// If the command is empty, use the command from the step
		command := inputs.command
		if len(c.Command) > 0 {
			command = strings.Join(append(c.Command, c.Args...), " ")
		}
		c.Command = []string{"/workspace/buildkite-agent"}
		c.Args = []string{"bootstrap"}
		c.ImagePullPolicy = corev1.PullAlways
		c.Env = append(c.Env, containerEnv...)
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "BUILDKITE_COMMAND",
				Value: command,
			},
			corev1.EnvVar{
				Name:  "BUILDKITE_CONTAINER_ID",
				Value: strconv.Itoa(i + systemContainerCount),
			},
		)

		w.cfg.DefaultCommandParams.ApplyTo(&c)
		if inputs.k8sPlugin != nil {
			inputs.k8sPlugin.CommandParams.ApplyTo(&c)
		}

		if c.Name == "" {
			c.Name = fmt.Sprintf("%s-%d", "container", i)
		}

		if c.WorkingDir == "" {
			c.WorkingDir = "/workspace"
		}
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		if inputs.k8sPlugin != nil {
			c.EnvFrom = append(c.EnvFrom, inputs.k8sPlugin.GitEnvFrom...)
		}
		podSpec.Containers[i] = c
	}

	containerCount := len(podSpec.Containers) + systemContainerCount

	if len(podSpec.Containers) == 0 {
		// Create a default command container named "container-0".
		c := corev1.Container{
			Name:            "container-0",
			Image:           w.cfg.Image,
			Command:         []string{"/workspace/buildkite-agent"},
			Args:            []string{"bootstrap"},
			WorkingDir:      "/workspace",
			VolumeMounts:    volumeMounts,
			ImagePullPolicy: corev1.PullAlways,
			Env: append(containerEnv,
				corev1.EnvVar{
					Name:  "BUILDKITE_COMMAND",
					Value: inputs.command,
				},
				corev1.EnvVar{
					Name:  "BUILDKITE_CONTAINER_ID",
					Value: strconv.Itoa(0 + systemContainerCount),
				},
			),
		}
		w.cfg.DefaultCommandParams.ApplyTo(&c)
		if inputs.k8sPlugin != nil {
			inputs.k8sPlugin.CommandParams.ApplyTo(&c)
			c.EnvFrom = append(c.EnvFrom, inputs.k8sPlugin.GitEnvFrom...)
		}
		podSpec.Containers = append(podSpec.Containers, c)
	}

	if inputs.k8sPlugin != nil {
		for i, c := range inputs.k8sPlugin.Sidecars {
			if c.Name == "" {
				c.Name = fmt.Sprintf("%s-%d", "sidecar", i)
			}
			c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
			w.cfg.DefaultSidecarParams.ApplyTo(&c)
			inputs.k8sPlugin.SidecarParams.ApplyTo(&c)
			c.EnvFrom = append(c.EnvFrom, inputs.k8sPlugin.GitEnvFrom...)
			podSpec.Containers = append(podSpec.Containers, c)
		}
	}

	agentTags := []agentTag{
		{
			Name:  "k8s:agent-stack-version",
			Value: version.Version(),
		},
	}

	if tags, err := agentTagsFromJob(inputs.agentQueryRules); err != nil {
		w.logger.Warn("error parsing job tags", zap.String("job", inputs.uuid))
	} else {
		agentTags = append(agentTags, tags...)
	}

	// agent server container
	agentContainer := corev1.Container{
		Name:            AgentContainerName,
		Args:            []string{"start"},
		Image:           w.cfg.Image,
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{
				Name:  "BUILDKITE_KUBERNETES_EXEC",
				Value: "true",
			},
			{
				Name:  "BUILDKITE_CONTAINER_COUNT",
				Value: strconv.Itoa(containerCount),
			},
			{
				Name:  "BUILDKITE_AGENT_TAGS",
				Value: createAgentTagString(agentTags),
			},
			{
				Name: "BUILDKITE_K8S_NODE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.nodeName",
					},
				},
			},
			{
				Name: "BUILDKITE_K8S_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name: "BUILDKITE_K8S_SERVICE_ACCOUNT",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.serviceAccountName",
					},
				},
			},
		},
	}
	agentContainer.Env = append(agentContainer.Env, env...)
	podSpec.Containers = append(podSpec.Containers, agentContainer)

	if !skipCheckout {
		podSpec.Containers = append(podSpec.Containers,
			w.createCheckoutContainer(podSpec, env, volumeMounts, inputs.k8sPlugin),
		)
	}

	podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
		Name:            CopyAgentContainerName,
		Image:           w.cfg.Image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"cp"},
		Args: []string{
			"/usr/local/bin/buildkite-agent",
			"/workspace",
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "workspace",
				MountPath: "/workspace",
			},
		},
	})
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: "workspace",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
	podSpec.RestartPolicy = corev1.RestartPolicyNever

	// Allow podSpec to be overridden by the agent configuration and the k8s plugin

	// Patch from the agent is applied first
	if w.cfg.PodSpecPatch != nil {
		w.logger.Info("applying podSpec patch from agent")
		patched, err := PatchPodSpec(podSpec, w.cfg.PodSpecPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from agent: %w", err)
		}
		podSpec = patched
	}

	if inputs.k8sPlugin != nil && inputs.k8sPlugin.PodSpecPatch != nil {
		w.logger.Info("applying podSpec patch from k8s plugin")
		patched, err := PatchPodSpec(podSpec, inputs.k8sPlugin.PodSpecPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from k8s plugin: %w", err)
		}
		podSpec = patched
	}

	kjob.Spec.Template.Spec = *podSpec

	return kjob, nil
}

var ErrNoCommandModification = errors.New("modifying container commands or args via podSpecPatch is not supported. Specify the command in the job's command field instead")

func PatchPodSpec(original *corev1.PodSpec, patch *corev1.PodSpec) (*corev1.PodSpec, error) {
	// We do special stuff™️ with container commands to make them run as buildkite agent things under the hood, so don't
	// let users mess with them via podSpecPatch.
	for _, c := range patch.Containers {
		if len(c.Command) != 0 || len(c.Args) != 0 {
			return nil, ErrNoCommandModification
		}
	}

	originalJSON, err := json.Marshal(original)
	if err != nil {
		return nil, fmt.Errorf("error converting original to JSON: %w", err)
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("error converting patch to JSON: %w", err)
	}

	patchedJSON, err := strategicpatch.StrategicMergePatch(originalJSON, patchJSON, corev1.PodSpec{})
	if err != nil {
		return nil, fmt.Errorf("error applying strategic patch: %w", err)
	}

	var patchedSpec corev1.PodSpec
	if err := json.Unmarshal(patchedJSON, &patchedSpec); err != nil {
		return nil, fmt.Errorf("error converting patched JSON to PodSpec: %w", err)
	}

	return &patchedSpec, nil
}

func (w *worker) createCheckoutContainer(
	podSpec *corev1.PodSpec,
	env []corev1.EnvVar,
	volumeMounts []corev1.VolumeMount,
	k8sPlugin *KubernetesPlugin,
) corev1.Container {
	checkoutContainer := corev1.Container{
		Name:            CheckoutContainerName,
		Image:           w.cfg.Image,
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{
				Name:  "BUILDKITE_KUBERNETES_EXEC",
				Value: "true",
			},
			{
				Name:  "BUILDKITE_BOOTSTRAP_PHASES",
				Value: "plugin,checkout",
			},
			{
				Name:  "BUILDKITE_AGENT_NAME",
				Value: "buildkite",
			},
			{
				Name:  "BUILDKITE_CONTAINER_ID",
				Value: "0",
			},
			{
				Name:  "BUILDKITE_PLUGINS_PATH",
				Value: "/workspace/plugins",
			},
		},
	}

	w.cfg.DefaultCheckoutParams.ApplyTo(&checkoutContainer)
	if k8sPlugin != nil {
		k8sPlugin.CheckoutParams.ApplyTo(&checkoutContainer)
		checkoutContainer.EnvFrom = append(checkoutContainer.EnvFrom, k8sPlugin.GitEnvFrom...)
	}

	checkoutContainer.Env = append(checkoutContainer.Env, env...)

	podUser, podGroup := int64(0), int64(0)
	if podSpec.SecurityContext != nil {
		if podSpec.SecurityContext.RunAsUser != nil {
			podUser = *(podSpec.SecurityContext.RunAsUser)
		}
		if podSpec.SecurityContext.RunAsGroup != nil {
			podGroup = *(podSpec.SecurityContext.RunAsGroup)
		}
	}

	// Ensure that the checkout occurs as the user/group specified in the pod's security context.
	// we will create a buildkite-agent user/group in the checkout container as needed and switch
	// to it. The created user/group will have the uid/gid specified in the pod's security context.
	switch {
	case podUser != 0 && podGroup != 0:
		// The checkout container needs to be run as root to create the user. After that, it switches to the user.
		checkoutContainer.SecurityContext = &corev1.SecurityContext{
			RunAsUser:    ptr.To[int64](0),
			RunAsGroup:   ptr.To[int64](0),
			RunAsNonRoot: ptr.To[bool](false),
		}

		checkoutContainer.Command = []string{"ash", "-c"}
		checkoutContainer.Args = []string{fmt.Sprintf(`set -exufo pipefail
addgroup -g %d buildkite-agent
adduser -D -u %d -G buildkite-agent -h /workspace buildkite-agent
su buildkite-agent -c "buildkite-agent-entrypoint bootstrap"`,
			podGroup,
			podUser,
		)}

	case podUser != 0 && podGroup == 0:
		// The checkout container needs to be run as root to create the user. After that, it switches to the user.
		checkoutContainer.SecurityContext = &corev1.SecurityContext{
			RunAsUser:    ptr.To[int64](0),
			RunAsGroup:   ptr.To[int64](0),
			RunAsNonRoot: ptr.To[bool](false),
		}

		checkoutContainer.Command = []string{"ash", "-c"}
		checkoutContainer.Args = []string{fmt.Sprintf(`set -exufo pipefail
adduser -D -u %d -G root -h /workspace buildkite-agent
su buildkite-agent -c "buildkite-agent-entrypoint bootstrap"`,
			podUser,
		)}

	// If the group is not root, but the user is root, I don't think we NEED to do anything. It's fine
	// for the user and group to be root for the checked out repo, even though the Pod's security
	// context has a non-root group.
	default:
		checkoutContainer.SecurityContext = nil
		// these are the default, but that default is sepciifed in the agent repo, so lets make it explicit
		checkoutContainer.Command = []string{"buildkite-agent-entrypoint"}
		checkoutContainer.Args = []string{"bootstrap"}
	}

	return checkoutContainer
}

func (w *worker) BuildFailureJob(err error, inputs buildInputs) (*batchv1.Job, error) {
	command := fmt.Sprintf("echo %q && exit 1", err.Error())
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				// the configured agent image may be private. If there is an error in specifying the
				// secrets for this image, we should still be able to run the failure job. So, we
				// bypass the potentially private image and use a public one. We could use a
				// thinner public image like `alpine:latest`, but it's generally unwise to depend
				// on an image that's not published by us.
				Image:   config.DefaultAgentImage,
				Command: []string{command},
			},
		},
	}
	// Command is overridden with the "print error and exit" command above.
	// k8sPlugin and otherPlugins are disabled for the failure job.
	return w.Build(podSpec, true, buildInputs{
		uuid:            inputs.uuid,
		command:         command,
		agentQueryRules: inputs.agentQueryRules,
		envMap:          inputs.envMap,
		k8sPlugin:       nil,
		otherPlugins:    nil,
	})
}

func (w *worker) labelWithAgentTags(dstLabels map[string]string, agentQueryRules []string) {
	ls, errs := agenttags.ToLabels(agentQueryRules)
	if len(errs) != 0 {
		w.logger.Warn("converting all tags to labels", zap.Errors("errs", errs))
	}

	for k, v := range ls {
		dstLabels[k] = v
	}
}

func (w *worker) annotateWithJobURL(dstAnnotations map[string]string, jobUUID string, envMap map[string]string) {
	buildURL := envMap["BUILDKITE_BUILD_URL"]
	u, err := url.Parse(buildURL)
	if err != nil {
		w.logger.Warn(
			"could not parse BuildURL when annotating with JobURL",
			zap.String("buildURL", buildURL),
		)
		return
	}
	u.Fragment = jobUUID
	dstAnnotations[config.JobURLAnnotation] = u.String()
}

func kjobName(jobUUID string) string {
	return fmt.Sprintf("buildkite-%s", jobUUID)
}

type agentTag struct {
	Name  string
	Value string
}

func agentTagsFromJob(agentQueryRules []string) ([]agentTag, error) {
	agentTags := make([]agentTag, 0, len(agentQueryRules))
	for _, tag := range agentQueryRules {
		k, v, found := strings.Cut(tag, "=")
		if !found {
			return nil, fmt.Errorf("could not parse tag: %q", tag)
		}
		agentTags = append(agentTags, agentTag{Name: k, Value: v})
	}

	return agentTags, nil
}

func createAgentTagString(tags []agentTag) string {
	var sb strings.Builder
	for i, t := range tags {
		sb.WriteString(t.Name)
		sb.WriteString("=")
		sb.WriteString(t.Value)
		if i < len(tags)-1 {
			sb.WriteString(",")
		}
	}
	return sb.String()
}
