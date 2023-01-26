package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent/v3/clicommand"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
)

const (
	agentTokenKey = "BUILDKITE_AGENT_TOKEN"
)

func Run(ctx context.Context, logger *zap.Logger, queue <-chan monitor.Job, client kubernetes.Interface, cfg api.Config) error {
	worker := worker{
		ctx:    ctx,
		cfg:    cfg,
		client: client,
		logger: logger.Named("worker"),
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-queue:
			if job.Err != nil {
				return job.Err
			}
			worker.Create(&job)
		}
	}
}

type KubernetesPlugin struct {
	PodSpec    *corev1.PodSpec
	GitEnvFrom []corev1.EnvFromSource
}

func (w *worker) k8sify(
	job *monitor.Job,
	tokenSecret string,
) (*batchv1.Job, error) {
	envMap := map[string]string{}
	for _, val := range job.Env {
		parts := strings.SplitN(val, "=", 2)
		envMap[parts[0]] = parts[1]
	}

	kjob := &batchv1.Job{}
	var plugins []map[string]json.RawMessage
	if pluginsJson, ok := envMap["BUILDKITE_PLUGINS"]; ok {
		if err := json.Unmarshal([]byte(pluginsJson), &plugins); err != nil {
			w.logger.Debug("invalid plugin spec", zap.String("json", pluginsJson))
			return nil, fmt.Errorf("err parsing plugins: %w", err)
		}
	}
	var (
		k8sPlugin    KubernetesPlugin
		otherPlugins []map[string]json.RawMessage
	)
	for _, plugin := range plugins {
		if len(plugin) != 1 {
			return nil, fmt.Errorf("found invalid plugin: %v", plugin)
		}
		if val, ok := plugin["github.com/buildkite-plugins/kubernetes-buildkite-plugin"]; ok {
			if err := json.Unmarshal(val, &k8sPlugin); err != nil {
				return nil, fmt.Errorf("err parsing kubernetes plugin: %w", err)
			}
		} else {
			for k, v := range plugin {
				otherPlugins = append(otherPlugins, map[string]json.RawMessage{k: v})
			}
		}
	}
	if k8sPlugin.PodSpec != nil {
		kjob.Spec.Template.Spec = *k8sPlugin.PodSpec
	} else {
		kjob.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Image:   w.cfg.Image,
				Command: []string{job.Command},
			},
		}
	}
	kjob.Name = kjobName(job)
	kjob.Labels = map[string]string{
		api.UUIDLabel: job.Uuid,
		api.TagLabel:  api.TagToLabel(job.Tag),
	}
	kjob.Spec.BackoffLimit = pointer.Int32(0)
	var env []corev1.EnvVar
	env = append(env, corev1.EnvVar{
		Name:  "BUILDKITE_BUILD_PATH",
		Value: "/workspace/build",
	}, corev1.EnvVar{
		Name:  "BUILDKITE_BIN_PATH",
		Value: "/workspace",
	}, corev1.EnvVar{
		Name: "BUILDKITE_AGENT_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: tokenSecret},
				Key:                  agentTokenKey,
			},
		},
	}, corev1.EnvVar{
		Name:  "BUILDKITE_AGENT_ACQUIRE_JOB",
		Value: job.Uuid,
	})
	if otherPlugins != nil {
		otherPluginsJson, err := json.Marshal(otherPlugins)
		if err != nil {
			return nil, fmt.Errorf("failed to remarshal non-k8s plugins: %w", err)
		}
		env = append(env, corev1.EnvVar{
			Name:  "BUILDKITE_PLUGINS",
			Value: string(otherPluginsJson),
		})
	}
	for k, v := range envMap {
		switch k {
		case "BUILDKITE_PLUGINS": //noop
		case "BUILDKITE_COMMAND": //noop
		case "BUILDKITE_ARTIFACT_PATHS": //noop
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
		podSpec.Containers[i] = c
	}

	containerCount := len(podSpec.Containers) + systemContainers
	if artifactPaths, found := envMap["BUILDKITE_ARTIFACT_PATHS"]; found && artifactPaths != "" {
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
	// agent server container
	agentContainer := corev1.Container{
		Name:            "agent",
		Args:            []string{"start"},
		Image:           w.cfg.Image,
		WorkingDir:      "/workspace",
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{
				Name:  "BUILDKITE_AGENT_EXPERIMENT",
				Value: "kubernetes-exec",
			}, {
				Name:  "BUILDKITE_CONTAINER_COUNT",
				Value: strconv.Itoa(containerCount),
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
			Value: "checkout",
		}, {
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		}, {
			Name:  "BUILDKITE_CONTAINER_ID",
			Value: "0",
		}},
		EnvFrom: k8sPlugin.GitEnvFrom,
	}
	checkoutContainer.Env = append(checkoutContainer.Env, env...)
	podSpec.Containers = append(podSpec.Containers, agentContainer, checkoutContainer)
	podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
		Name:            "copy-agent",
		Image:           w.cfg.Image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"cp"},
		Args:            []string{"/usr/local/bin/buildkite-agent", "/workspace"},
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

type worker struct {
	ctx    context.Context
	cfg    api.Config
	client kubernetes.Interface
	logger *zap.Logger
}

func (w *worker) Create(job *monitor.Job) {
	logger := w.logger.With(zap.String("uuid", job.Uuid))
	logger.Debug("creating job")
	kjob, err := w.k8sify(job, w.cfg.AgentTokenSecret)
	if err != nil {
		logger.Error("failed to convert job to pod", zap.Error(err))
		return
	}
	logger = logger.With(zap.String("kjob", kjob.Name))
	_, err = w.client.BatchV1().Jobs(w.cfg.Namespace).Create(w.ctx, kjob, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			logger.Debug("job already exists")
		} else {
			logger.Error("failed to create job", zap.Error(err))
		}
		return
	}
}

func kjobName(job *monitor.Job) string {
	return fmt.Sprintf("buildkite-%s", job.Uuid)
}
