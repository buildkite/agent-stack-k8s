package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

type Config struct {
	Namespace              string
	Image                  string
	AgentToken             string
	JobTTL                 time.Duration
	AdditionalRedactedVars []string
	PodSpecPatch           *corev1.PodSpec
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
	Checkout          Checkout               `json:"checkout,omitempty"`
}

type Checkout struct {
	Skip       bool   `json:"skip,omitempty"`
	CloneFlags string `json:"cloneFlags,omitempty"`
	FetchFlags string `json:"fetchFlags,omitempty"`
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
	jobWrapper := NewJobWrapper(w.logger, job, w.cfg).ParsePlugins()
	kjob, err := jobWrapper.Build(false)
	if err != nil {
		logger.Warn("Job definition error detected, creating failure job instead", zap.Error(err))
		kjob, err = jobWrapper.BuildFailureJob(err)
		if err != nil {
			return fmt.Errorf("job definition error and failure job definition error: %w", err)
		}
	}

	_, err = w.client.BatchV1().Jobs(w.cfg.Namespace).Create(ctx, kjob, metav1.CreateOptions{})
	if err != nil {
		if kerrors.IsInvalid(err) {
			kjob, err = jobWrapper.BuildFailureJob(err)
			if err != nil {
				return fmt.Errorf("job registration error and failure job definition error: %w", err)
			}
			_, err = w.client.BatchV1().Jobs(w.cfg.Namespace).Create(ctx, kjob, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("job registration error and failure job registration error: %w", err)
			}
			return nil
		} else {
			return err
		}
	}
	return nil
}

type jobWrapper struct {
	logger       *zap.Logger
	job          *api.CommandJob
	envMap       map[string]string
	err          error
	k8sPlugin    KubernetesPlugin
	otherPlugins []map[string]json.RawMessage
	cfg          Config
}

func NewJobWrapper(logger *zap.Logger, job *api.CommandJob, config Config) *jobWrapper {
	return &jobWrapper{
		logger: logger,
		job:    job,
		cfg:    config,
		envMap: make(map[string]string),
	}
}

func (w *jobWrapper) ParsePlugins() *jobWrapper {
	for _, val := range w.job.Env {
		parts := strings.SplitN(val, "=", 2)
		w.envMap[parts[0]] = parts[1]
	}
	var plugins []map[string]json.RawMessage
	if pluginsJson, ok := w.envMap["BUILDKITE_PLUGINS"]; ok {
		if err := json.Unmarshal([]byte(pluginsJson), &plugins); err != nil {
			w.logger.Debug("invalid plugin spec", zap.String("json", pluginsJson))
			w.err = fmt.Errorf("failed parsing plugins: %w", err)
			return w
		}
	}
	w.logger.Info("parsing", zap.Any("plugins", plugins))
	for _, plugin := range plugins {
		if len(plugin) != 1 {
			w.err = fmt.Errorf("found invalid plugin: %v", plugin)
			return w
		}
		if val, ok := plugin["github.com/buildkite-plugins/kubernetes-buildkite-plugin"]; ok {
			if err := json.Unmarshal(val, &w.k8sPlugin); err != nil {
				w.err = fmt.Errorf("failed parsing Kubernetes plugin: %w", err)
				return w
			}
		} else {
			for k, v := range plugin {
				w.otherPlugins = append(w.otherPlugins, map[string]json.RawMessage{k: v})
			}
		}
	}
	return w
}

// Build builds a job. The checkout container will be skipped either by passing
// `true` or if the k8s plugin configuration is configured to skip it.
func (w *jobWrapper) Build(skipCheckout bool) (*batchv1.Job, error) {
	// if previous steps have failed, error immediately
	if w.err != nil {
		return nil, w.err
	}

	skipCheckout = skipCheckout || w.k8sPlugin.Checkout.Skip

	kjob := &batchv1.Job{}
	kjob.Name = kjobName(w.job)

	// Populate the job's podSpec from the step's k8s plugin, or use the command in the step
	if w.k8sPlugin.PodSpec != nil {
		kjob.Spec.Template.Spec = *w.k8sPlugin.PodSpec
	} else {
		kjob.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Image:   w.cfg.Image,
				Command: []string{w.job.Command},
			},
		}
	}

	if w.k8sPlugin.Metadata.Labels == nil {
		w.k8sPlugin.Metadata.Labels = map[string]string{}
	}

	if w.k8sPlugin.Metadata.Annotations == nil {
		w.k8sPlugin.Metadata.Annotations = map[string]string{}
	}

	w.k8sPlugin.Metadata.Labels[config.UUIDLabel] = w.job.Uuid
	w.labelWithAgentTags()
	w.k8sPlugin.Metadata.Annotations[config.BuildURLAnnotation] = w.envMap["BUILDKITE_BUILD_URL"]
	w.annotateWithJobURL()

	// Prevent k8s cluster autoscaler from terminating the job before it finishes to scale down cluster
	w.k8sPlugin.Metadata.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"] = "false"

	kjob.Labels = w.k8sPlugin.Metadata.Labels
	kjob.Spec.Template.Labels = w.k8sPlugin.Metadata.Labels
	kjob.Annotations = w.k8sPlugin.Metadata.Annotations
	kjob.Spec.Template.Annotations = w.k8sPlugin.Metadata.Annotations
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
			Value: w.job.Uuid,
		},
	}
	if w.otherPlugins != nil {
		otherPluginsJson, err := json.Marshal(w.otherPlugins)
		if err != nil {
			return nil, fmt.Errorf("failed to remarshal non-k8s plugins: %w", err)
		}
		env = append(env, corev1.EnvVar{
			Name:  "BUILDKITE_PLUGINS",
			Value: string(otherPluginsJson),
		})
	}
	for k, v := range w.envMap {
		switch k {
		case "BUILDKITE_COMMAND", "BUILDKITE_ARTIFACT_PATHS", "BUILDKITE_PLUGINS": // noop
		default:
			env = append(env, corev1.EnvVar{Name: k, Value: v})
		}
	}

	redactedVars := append(w.cfg.AdditionalRedactedVars, clicommand.RedactedVars.Value.Value()...)

	volumeMounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	volumeMounts = append(volumeMounts, w.k8sPlugin.ExtraVolumeMounts...)

	systemContainerCount := 0
	if !skipCheckout {
		systemContainerCount = 1
	}

	ttl := int32(w.cfg.JobTTL.Seconds())
	kjob.Spec.TTLSecondsAfterFinished = &ttl

	podSpec := &kjob.Spec.Template.Spec

	containerEnv := append([]corev1.EnvVar{}, env...)
	containerEnv = append(containerEnv, []corev1.EnvVar{
		{
			Name:  "BUILDKITE_AGENT_EXPERIMENT",
			Value: "kubernetes-exec",
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
			Name:  clicommand.RedactedVars.EnvVar,
			Value: strings.Join(redactedVars, ","),
		},
		{
			Name:  "BUILDKITE_SHELL",
			Value: "/bin/sh -ec",
		},
		{
			Name:  "BUILDKITE_ARTIFACT_PATHS",
			Value: w.envMap["BUILDKITE_ARTIFACT_PATHS"],
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
		command := w.job.Command
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

		if c.Name == "" {
			c.Name = fmt.Sprintf("%s-%d", "container", i)
		}

		if c.WorkingDir == "" {
			c.WorkingDir = "/workspace"
		}
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		c.EnvFrom = append(c.EnvFrom, w.k8sPlugin.GitEnvFrom...)
		podSpec.Containers[i] = c
	}

	containerCount := len(podSpec.Containers) + systemContainerCount

	if len(podSpec.Containers) == 0 {
		podSpec.Containers = append(podSpec.Containers, corev1.Container{
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
					Value: w.job.Command,
				},
				corev1.EnvVar{
					Name:  "BUILDKITE_CONTAINER_ID",
					Value: strconv.Itoa(0 + systemContainerCount),
				},
			),
			EnvFrom: w.k8sPlugin.GitEnvFrom,
		})
	}

	for i, c := range w.k8sPlugin.Sidecars {
		if c.Name == "" {
			c.Name = fmt.Sprintf("%s-%d", "sidecar", i)
		}
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		c.EnvFrom = append(c.EnvFrom, w.k8sPlugin.GitEnvFrom...)
		podSpec.Containers = append(podSpec.Containers, c)
	}

	agentTags := []agentTag{
		{
			Name:  "k8s:agent-stack-version",
			Value: version.Version(),
		},
	}

	if tags, err := agentTagsFromJob(w.job); err != nil {
		w.logger.Warn("error parsing job tags", zap.String("job", w.job.Uuid))
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
				Name:  "BUILDKITE_AGENT_EXPERIMENT",
				Value: "kubernetes-exec",
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
		podSpec.Containers = append(podSpec.Containers, w.createCheckoutContainer(kjob, env, volumeMounts))
	}

	podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
		Name:            "copy-agent",
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
	var err error
	if w.cfg.PodSpecPatch != nil {
		w.logger.Info("applying podSpec patch from agent")
		podSpec, err = PatchPodSpec(podSpec, w.cfg.PodSpecPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from agent: %w", err)
		}
	}

	if w.k8sPlugin.PodSpecPatch != nil {
		w.logger.Info("applying podSpec patch from k8s plugin")
		podSpec, err = PatchPodSpec(podSpec, w.k8sPlugin.PodSpecPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to apply podSpec patch from k8s plugin: %w", err)
		}
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

func (w *jobWrapper) createCheckoutContainer(
	kjob *batchv1.Job,
	env []corev1.EnvVar,
	volumeMounts []corev1.VolumeMount,
) corev1.Container {
	checkoutContainer := corev1.Container{
		Name:            "checkout",
		Image:           w.cfg.Image,
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{
				Name:  "BUILDKITE_AGENT_EXPERIMENT",
				Value: "kubernetes-exec",
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
			{
				Name:  "BUILDKITE_GIT_CLONE_FLAGS",
				Value: w.k8sPlugin.Checkout.CloneFlags,
			},
			{
				Name:  "BUILDKITE_GIT_FETCH_FLAGS",
				Value: w.k8sPlugin.Checkout.FetchFlags,
			},
		},
		EnvFrom: w.k8sPlugin.GitEnvFrom,
	}
	checkoutContainer.Env = append(checkoutContainer.Env, env...)

	podUser, podGroup := int64(0), int64(0)
	if kjob.Spec.Template.Spec.SecurityContext != nil {
		if kjob.Spec.Template.Spec.SecurityContext.RunAsUser != nil {
			podUser = *(w.k8sPlugin.PodSpec.SecurityContext.RunAsUser)
		}
		if kjob.Spec.Template.Spec.SecurityContext.RunAsGroup != nil {
			podGroup = *(w.k8sPlugin.PodSpec.SecurityContext.RunAsGroup)
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

func (w *jobWrapper) BuildFailureJob(err error) (*batchv1.Job, error) {
	w.err = nil
	w.k8sPlugin = KubernetesPlugin{
		PodSpec: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					// the configured agent image may be private. If there is an error in specifying the
					// secrets for this image, we should still be able to run the failure job. So, we
					// bypass the potentially private image and use a public one. We could use a
					// thinner public image like `alpine:latest`, but it's generally unwise to depend
					// on an image that's not published by us.
					Image:   config.DefaultAgentImage,
					Command: []string{fmt.Sprintf("echo %q && exit 1", err.Error())},
				},
			},
		},
	}
	w.otherPlugins = nil
	return w.Build(true)
}

func (w *jobWrapper) labelWithAgentTags() {
	labels, errs := agenttags.ToLabels(w.job.AgentQueryRules)
	if len(errs) != 0 {
		w.logger.Warn("converting all tags to labels", zap.Errors("errs", errs))
	}

	for k, v := range labels {
		w.k8sPlugin.Metadata.Labels[k] = v
	}
}

func (w *jobWrapper) annotateWithJobURL() {
	buildURL := w.envMap["BUILDKITE_BUILD_URL"]
	u, err := url.Parse(buildURL)
	if err != nil {
		w.logger.Warn(
			"could not parse BuildURL when annotating with JobURL",
			zap.String("buildURL", buildURL),
		)
		return
	}
	u.Fragment = w.job.Uuid
	w.k8sPlugin.Metadata.Annotations[config.JobURLAnnotation] = u.String()
}

func kjobName(job *api.CommandJob) string {
	return fmt.Sprintf("buildkite-%s", job.Uuid)
}

type agentTag struct {
	Name  string
	Value string
}

func agentTagsFromJob(j *api.CommandJob) ([]agentTag, error) {
	if j == nil {
		return nil, fmt.Errorf("job is nil")
	}

	agentTags := make([]agentTag, 0, len(j.AgentQueryRules))
	for _, tag := range j.AgentQueryRules {
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
