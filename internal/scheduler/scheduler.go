package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/monitor"
	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/buildkite/agent/v3/clicommand"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
)

const (
	agentTokenKey      = "BUILDKITE_AGENT_TOKEN"
	AgentContainerName = "agent"
)

func New(logger *zap.Logger, client kubernetes.Interface, cfg api.Config) *worker {
	return &worker{
		cfg:    cfg,
		client: client,
		logger: logger.Named("worker"),
	}
}

// returns an informer factory configured to watch resources (pods, jobs) created by the scheduler
func NewInformerFactory(k8s kubernetes.Interface, tags []string) (informers.SharedInformerFactory, error) {
	hasTag, err := labels.NewRequirement(api.TagLabel, selection.In, api.TagsToLabels(tags))
	if err != nil {
		return nil, fmt.Errorf("failed to build tag label selector for job manager: %w", err)
	}
	hasUUID, err := labels.NewRequirement(api.UUIDLabel, selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build uuid label selector for job manager: %w", err)
	}
	factory := informers.NewSharedInformerFactoryWithOptions(k8s, 0, informers.WithTweakListOptions(func(opt *metav1.ListOptions) {
		opt.LabelSelector = labels.NewSelector().Add(*hasTag, *hasUUID).String()
	}))
	return factory, nil
}

type KubernetesPlugin struct {
	PodSpec    *corev1.PodSpec
	GitEnvFrom []corev1.EnvFromSource
	Sidecars   []corev1.Container `json:"sidecars,omitempty"`
	Metadata   Metadata
}

type Metadata struct {
	Annotations map[string]string
	Labels      map[string]string
}

type worker struct {
	cfg    api.Config
	client kubernetes.Interface
	logger *zap.Logger
}

func (w *worker) Create(ctx context.Context, job *monitor.Job) error {
	logger := w.logger.With(zap.String("uuid", job.Uuid))
	logger.Info("creating job")
	jobWrapper := NewJobWrapper(w.logger, job, w.cfg).ParsePlugins()
	kjob, err := jobWrapper.Build()
	if err != nil {
		kjob, err = jobWrapper.BuildFailureJob(err)
		if err != nil {
			return fmt.Errorf("failed to create job: %w", err)
		}
	}
	_, err = w.client.BatchV1().Jobs(w.cfg.Namespace).Create(ctx, kjob, metav1.CreateOptions{})
	if err != nil {
		if errors.IsInvalid(err) {
			kjob, err = jobWrapper.BuildFailureJob(err)
			if err != nil {
				return fmt.Errorf("failed to create job: %w", err)
			}
			_, err = w.client.BatchV1().Jobs(w.cfg.Namespace).Create(ctx, kjob, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create job: %w", err)
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
	job          *monitor.Job
	envMap       map[string]string
	err          error
	k8sPlugin    KubernetesPlugin
	otherPlugins []map[string]json.RawMessage
	cfg          api.Config
}

func NewJobWrapper(logger *zap.Logger, job *monitor.Job, config api.Config) *jobWrapper {
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

func (w *jobWrapper) Build() (*batchv1.Job, error) {
	// if previous steps have failed, error immediately
	if w.err != nil {
		return nil, w.err
	}

	kjob := &batchv1.Job{}
	kjob.Name = kjobName(w.job)
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
		w.k8sPlugin.Metadata.Annotations = map[string]string{}
	}
	w.k8sPlugin.Metadata.Labels[api.UUIDLabel] = w.job.Uuid
	w.k8sPlugin.Metadata.Labels[api.TagLabel] = api.TagToLabel(w.job.Tag)
	w.k8sPlugin.Metadata.Annotations[api.BuildURLAnnotation] = w.envMap["BUILDKITE_BUILD_URL"]
	kjob.Labels = w.k8sPlugin.Metadata.Labels
	kjob.Spec.Template.Labels = w.k8sPlugin.Metadata.Labels
	kjob.Annotations = w.k8sPlugin.Metadata.Annotations
	kjob.Spec.Template.Annotations = w.k8sPlugin.Metadata.Annotations
	kjob.Spec.BackoffLimit = pointer.Int32(0)
	env := []corev1.EnvVar{
		{
			Name:  "BUILDKITE_BUILD_PATH",
			Value: "/workspace/build",
		}, {
			Name:  "BUILDKITE_BIN_PATH",
			Value: "/workspace",
		}, {
			Name: agentTokenKey,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: w.cfg.AgentTokenSecret},
					Key:                  agentTokenKey,
				},
			},
		}, {
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
		case "BUILDKITE_COMMAND", "BUILDKITE_ARTIFACT_PATHS", "BUILDKITE_PLUGINS": //noop
		default:
			env = append(env, corev1.EnvVar{Name: k, Value: v})
		}
	}
	volumeMounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	const systemContainers = 1
	ttl := int32(w.cfg.JobTTL.Seconds())
	kjob.Spec.TTLSecondsAfterFinished = &ttl

	podSpec := &kjob.Spec.Template.Spec
	for i, c := range podSpec.Containers {
		command := strings.Join(append(c.Command, c.Args...), " ")
		c.Command = []string{"/workspace/buildkite-agent"}
		c.Args = []string{"bootstrap"}
		c.ImagePullPolicy = corev1.PullAlways
		c.Env = append(c.Env, env...)
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "BUILDKITE_COMMAND",
			Value: command,
		}, corev1.EnvVar{
			Name:  "BUILDKITE_AGENT_EXPERIMENT",
			Value: "kubernetes-exec",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_BOOTSTRAP_PHASES",
			Value: "plugin,command",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_CONTAINER_ID",
			Value: strconv.Itoa(i + systemContainers),
		}, corev1.EnvVar{
			Name:  "BUILDKITE_PLUGINS_PATH",
			Value: "/tmp",
		}, corev1.EnvVar{
			Name:  clicommand.RedactedVars.EnvVar,
			Value: strings.Join(clicommand.RedactedVars.Value.Value(), ","),
		})
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

	containerCount := len(podSpec.Containers) + systemContainers

	for i, c := range w.k8sPlugin.Sidecars {
		if c.Name == "" {
			c.Name = fmt.Sprintf("%s-%d", "sidecar", i)
		}
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		c.EnvFrom = append(c.EnvFrom, w.k8sPlugin.GitEnvFrom...)
		podSpec.Containers = append(podSpec.Containers, c)
	}

	if artifactPaths, found := w.envMap["BUILDKITE_ARTIFACT_PATHS"]; found && artifactPaths != "" {
		artifactsContainer := corev1.Container{
			Name:            "upload-artifacts",
			Image:           w.cfg.Image,
			Args:            []string{"bootstrap"},
			WorkingDir:      "/workspace",
			VolumeMounts:    volumeMounts,
			ImagePullPolicy: corev1.PullAlways,
			Env: []corev1.EnvVar{{
				Name:  "BUILDKITE_AGENT_EXPERIMENT",
				Value: "kubernetes-exec",
			}, {
				Name:  "BUILDKITE_BOOTSTRAP_PHASES",
				Value: "command",
			}, {
				Name:  "BUILDKITE_COMMAND",
				Value: "true",
			}, {
				Name:  "BUILDKITE_AGENT_NAME",
				Value: "buildkite",
			}, {
				Name:  "BUILDKITE_CONTAINER_ID",
				Value: strconv.Itoa(containerCount),
			}, {
				Name:  "BUILDKITE_ARTIFACT_PATHS",
				Value: artifactPaths,
			}},
		}
		artifactsContainer.Env = append(artifactsContainer.Env, env...)
		containerCount++
		podSpec.Containers = append(podSpec.Containers, artifactsContainer)
	}

	agentTags := []agentTag{
		{
			Name:  "bk-k8s:version",
			Value: version.Version(),
		},
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
		},
	}
	agentContainer.Env = append(agentContainer.Env, env...)
	// system client container(s)
	checkoutContainer := corev1.Container{
		Name:            "checkout",
		Image:           w.cfg.Image,
		Args:            []string{"bootstrap"},
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{{
			Name:  "BUILDKITE_AGENT_EXPERIMENT",
			Value: "kubernetes-exec",
		}, {
			Name:  "BUILDKITE_BOOTSTRAP_PHASES",
			Value: "checkout,command",
		}, {
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		}, {
			Name:  "BUILDKITE_CONTAINER_ID",
			Value: "0",
		}, {
			Name:  "BUILDKITE_COMMAND",
			Value: "cp -r ~/.ssh /workspace/.ssh && chmod -R 777 /workspace",
		}},
		EnvFrom: w.k8sPlugin.GitEnvFrom,
	}
	checkoutContainer.Env = append(checkoutContainer.Env, env...)
	podSpec.Containers = append(podSpec.Containers, agentContainer, checkoutContainer)
	podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
		Name:            "copy-agent",
		Image:           w.cfg.Image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"cp"},
		Args:            []string{"/usr/local/bin/buildkite-agent", "/usr/local/bin/ssh-env-config.sh", "/workspace"},
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
	return kjob, nil
}

func (w *jobWrapper) BuildFailureJob(err error) (*batchv1.Job, error) {
	w.err = nil
	w.k8sPlugin = KubernetesPlugin{
		PodSpec: &corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image:   w.cfg.Image,
					Command: []string{fmt.Sprintf("echo %q && exit 1", err.Error())},
				},
			},
		},
	}
	w.otherPlugins = nil
	return w.Build()
}

func kjobName(job *monitor.Job) string {
	return fmt.Sprintf("buildkite-%s", job.Uuid)
}

type agentTag struct {
	Name  string
	Value string
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
