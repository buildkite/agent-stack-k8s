package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent/v3/agent/plugin"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var defaultBootstrapPod = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{
			defaultLabel: "true",
		},
	},
	Spec: corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		InitContainers: []corev1.Container{
			{
				Name:            "copy-agent",
				Image:           agentImage,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"cp"},
				Args:            []string{"/usr/local/bin/buildkite-agent", "/workspace"},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "workspace",
						MountPath: "/workspace",
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	},
}

const (
	ns           = "default"
	agentImage   = "benmoss/buildkite-agent:latest"
	defaultLabel = "bk-agent-stack-kubernetes"
)

type Config struct {
	Org,
	Pipeline,
	AgentToken string
	DeletePods bool
}

func Run(ctx context.Context, logger *zap.Logger, monitor *monitor.Monitor, cfg Config) error {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, nil)
	clientConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to create client config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create clienset: %w", err)
	}

	worker := worker{
		cfg:       cfg,
		clientset: clientset,
		logger:    logger.Named("worker"),
	}

	scheduled := monitor.Scheduled(ctx, cfg.Org, cfg.Pipeline)
	finished := monitor.Finished(ctx, cfg.Org, cfg.Pipeline)
	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-scheduled:
			worker.Create(ctx, &job)
		case job := <-finished:
			worker.Cleanup(ctx, &job)
		}
	}
}

func podFromJob(
	job *api.CommandJob,
	token string,
) (*corev1.Pod, error) {
	envMap := map[string]string{}
	for _, val := range job.Env {
		parts := strings.Split(val, "=")
		envMap[parts[0]] = parts[1]
	}

	pod := defaultBootstrapPod.DeepCopy()
	if envMap["BUILDKITE_PLUGINS"] == "" {
		return nil, fmt.Errorf("no plugins found")
	}
	plugins, err := plugin.CreateFromJSON(envMap["BUILDKITE_PLUGINS"])
	if err != nil {
		return nil, fmt.Errorf("err parsing plugins: %w", err)
	}
	for _, plugin := range plugins {
		asJson, err := json.Marshal(plugin.Configuration)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		if err := json.Unmarshal(asJson, &pod.Spec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}
	pod.Name = podName(job)
	var env []corev1.EnvVar
	env = append(env, corev1.EnvVar{
		Name:  "BUILDKITE_BUILD_PATH",
		Value: "/workspace/build",
	}, corev1.EnvVar{
		Name:  "BUILDKITE_AGENT_TOKEN",
		Value: token,
	}, corev1.EnvVar{
		Name:  "BUILDKITE_AGENT_ACQUIRE_JOB",
		Value: job.Uuid,
	})
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

	for i, c := range pod.Spec.Containers {
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
			Value: "command",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_CONTAINER_ID",
			Value: strconv.Itoa(i + systemContainers),
		})
		if c.Name == "" {
			c.Name = fmt.Sprintf("%s-%d", "container", i)
		}
		if c.WorkingDir == "" {
			c.WorkingDir = "/workspace"
		}
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		pod.Spec.Containers[i] = c
	}

	containerCount := len(pod.Spec.Containers) + systemContainers
	if artifactPaths, found := envMap["BUILDKITE_ARTIFACT_PATHS"]; found && artifactPaths != "" {
		artifactsContainer := corev1.Container{
			Name:            "upload-artifacts",
			Image:           agentImage,
			Command:         []string{"/workspace/buildkite-agent"},
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
		pod.Spec.Containers = append(pod.Spec.Containers, artifactsContainer)
	}
	// agent server container
	agentContainer := corev1.Container{
		Name:            "agent",
		Command:         []string{"/workspace/buildkite-agent"},
		Args:            []string{"start"},
		Image:           agentImage,
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
		Image:           agentImage,
		Command:         []string{"/workspace/buildkite-agent"},
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
	}
	checkoutContainer.Env = append(checkoutContainer.Env, env...)
	pod.Spec.Containers = append(pod.Spec.Containers, agentContainer, checkoutContainer)
	return pod, nil
}

type worker struct {
	cfg       Config
	clientset *kubernetes.Clientset
	logger    *zap.Logger
}

func (w *worker) Create(ctx context.Context, job *api.CommandJob) {
	logger := w.logger.With(zap.String("job", job.Uuid), zap.String("label", job.Label))
	pod, err := podFromJob(job, w.cfg.AgentToken)
	if err != nil {
		logger.Error("failed to convert job to pod", zap.Error(err))
		return
	}
	logger = logger.With(zap.String("pod", pod.Name))
	_, err = w.clientset.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		logger.Error("failed to create pod", zap.Error(err))
		return
	}
	logger.Debug("created pod")
}

func (w *worker) Cleanup(ctx context.Context, job *api.CommandJob) {
	name := podName(job)
	logger := w.logger.With(zap.String("job", job.Uuid), zap.String("label", job.Label), zap.String("pod", name))
	if w.cfg.DeletePods {
		if err := w.clientset.CoreV1().Pods(ns).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
			logger.Error("failed to delete pod", zap.Error(err))
			return
		}
		logger.Debug("deleted pod")
	}
}
func podName(job *api.CommandJob) string {
	return fmt.Sprintf("buildkite-%s", job.Uuid)
}
