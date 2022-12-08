package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent/v3/agent/plugin"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
)

const (
	ns           = "default"
	agentImage   = "benmoss/buildkite-agent:latest"
	defaultLabel = "bk-agent-stack-kubernetes"
)

type Config struct {
	JobTTL time.Duration
	Client graphql.Client
	Org    string
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
	tokenResp, err := api.CreateAgentToken(ctx, cfg.Client, cfg.Org)
	if err != nil {
		zap.L().Fatal("failed to create agent token", zap.Error(err))
	}
	defer func() {
		if _, err := api.AgentTokenRevoke(context.Background(), cfg.Client, api.AgentTokenRevokeInput{
			Id:     tokenResp.AgentTokenCreate.AgentTokenEdge.Node.Id,
			Reason: "agent shutdown",
		}); err != nil {
			zap.L().Error("failed to revoke token", zap.Error(err))
		}
	}()

	worker := worker{
		ctx:        ctx,
		cfg:        cfg,
		clientset:  clientset,
		agentToken: tokenResp.AgentTokenCreate.AgentTokenEdge.Node.Token,
		logger:     logger.Named("worker"),
		done:       make(chan string),
	}
	requirement, err := labels.NewRequirement(defaultLabel, selection.Exists, nil)
	if err != nil {
		return fmt.Errorf("invalid requirement: %w", err)
	}
	selector := labels.NewSelector().Add(*requirement)
	go worker.watchCompletions(ctx, selector)

	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-monitor.Scheduled():
			if job.Err != nil {
				return job.Err
			}
			worker.Create(&job.CommandJob)
		case uuid := <-worker.done:
			monitor.Done(uuid)
		}
	}
}

type PluginConfig struct {
	PodSpec    corev1.PodSpec
	GitEnvFrom []corev1.EnvFromSource
}

func (w *worker) k8sify(
	job *api.CommandJob,
	token string,
) (*batchv1.Job, error) {
	envMap := map[string]string{}
	for _, val := range job.Env {
		parts := strings.Split(val, "=")
		envMap[parts[0]] = parts[1]
	}

	var pluginConfig PluginConfig
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
		if err := json.Unmarshal(asJson, &pluginConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}
	kjob := &batchv1.Job{}
	kjob.Spec.Template.Spec = pluginConfig.PodSpec
	kjob.Name = kjobName(job)
	kjob.Labels = map[string]string{
		defaultLabel: job.Uuid,
	}
	kjob.Spec.BackoffLimit = pointer.Int32(0)
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
		podSpec.Containers[i] = c
	}

	containerCount := len(podSpec.Containers) + systemContainers
	if artifactPaths, found := envMap["BUILDKITE_ARTIFACT_PATHS"]; found && artifactPaths != "" {
		artifactsContainer := corev1.Container{
			Name:            "upload-artifacts",
			Image:           agentImage,
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
		EnvFrom: pluginConfig.GitEnvFrom,
	}
	checkoutContainer.Env = append(checkoutContainer.Env, env...)
	podSpec.Containers = append(podSpec.Containers, agentContainer, checkoutContainer)
	podSpec.InitContainers = append(podSpec.InitContainers, corev1.Container{
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
	ctx        context.Context
	cfg        Config
	clientset  *kubernetes.Clientset
	logger     *zap.Logger
	done       chan string
	agentToken string
}

func (w *worker) Create(job *api.CommandJob) {
	logger := w.logger.With(zap.String("job", job.Uuid), zap.String("label", job.Label))
	kjob, err := w.k8sify(job, w.agentToken)
	if err != nil {
		logger.Error("failed to convert job to pod", zap.Error(err))
		return
	}
	logger = logger.With(zap.String("kjob", kjob.Name))
	_, err = w.clientset.BatchV1().Jobs(ns).Create(w.ctx, kjob, metav1.CreateOptions{})
	if err != nil {
		logger.Error("failed to create job", zap.Error(err))
		return
	}
	logger.Debug("created job")
}

func (w *worker) watchCompletions(ctx context.Context, selector labels.Selector) {
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector.String()
			return w.clientset.BatchV1().Jobs(ns).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector.String()
			return w.clientset.BatchV1().Jobs(ns).Watch(ctx, options)
		},
	}

	_, controller := cache.NewInformer(lw, &batchv1.Job{}, 0, cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, newObj interface{}) {
			job := newObj.(*batchv1.Job)
			for _, condition := range job.Status.Conditions {
				if condition.Status == corev1.ConditionTrue {
					if condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed {
						uuid, found := job.Labels[defaultLabel]
						if !found {
							w.logger.Error("job found without label", zap.String("name", job.Name))
						} else {
							w.done <- uuid
						}
					}
				}
			}
		},
	})
	controller.Run(ctx.Done())
}

func kjobName(job *api.CommandJob) string {
	return fmt.Sprintf("buildkite-%s", job.Uuid)
}
