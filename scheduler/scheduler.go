package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent/v3/agent/plugin"
	"github.com/sanity-io/litter"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	toolswatch "k8s.io/client-go/tools/watch"
)

var defaultBootstrapPod = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		GenerateName: "agent-",
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
	ns         = "default"
	agentImage = "benmoss/buildkite-agent:latest"
)

func Run(ctx context.Context, token, org, pipeline, agentToken string) error {
	graphqlClient := api.NewClient(token)
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
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			slug := fmt.Sprintf("%s/%s", org, pipeline)
			log.Println("getting builds for pipeline", slug)
			buildsResponse, err := api.GetBuildsForPipelineBySlug(ctx, graphqlClient, slug)
			if err != nil {
				return fmt.Errorf("failed to fetch builds for pipeline: %w", err)
			}
			log.Println("got jobs", buildsResponse.Pipeline.Jobs)
			for _, job := range buildsResponse.Pipeline.Jobs.Edges {
				switch job := job.Node.(type) {
				case *api.GetBuildsForPipelineBySlugPipelineJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand:
					pod, err := podFromJob(job.CommandJob, agentToken)
					if err != nil {
						return fmt.Errorf("failed to convert job to pod: %w", err)
					}
					pod, err = clientset.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
					if err != nil {
						return fmt.Errorf("failed to create pod: %w", err)
					}
					log.Printf("created pod %q", pod.Name)
					fs := fields.OneTermEqualSelector(metav1.ObjectNameField, pod.Name)
					lw := &cache.ListWatch{
						ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
							options.FieldSelector = fs.String()
							return clientset.CoreV1().Pods(pod.Namespace).List(context.TODO(), options)
						},
						WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
							options.FieldSelector = fs.String()
							return clientset.CoreV1().Pods(ns).Watch(ctx, options)
						},
					}
					_, err = toolswatch.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, func(ev watch.Event) (bool, error) {
						if pod, ok := ev.Object.(*corev1.Pod); ok {
							// todo: handle image pull errors
							switch pod.Status.Phase {
							case corev1.PodPending, corev1.PodRunning, corev1.PodUnknown:
								log.Println(pod.Status.Phase)
								return false, nil
							case corev1.PodSucceeded:
								log.Println("pod success!")
								return true, nil
							case corev1.PodFailed:
								log.Println("pod failed!")
								return true, nil
							default:
								return false, fmt.Errorf("unexpected pod status: %s", pod.Status.Phase)
							}
						}
						return false, errors.New("event object not of type v1.Node")
					})
					if err != nil {
						if errors.Is(err, wait.ErrWaitTimeout) {
							log.Println("context canceled")
						} else {
							return fmt.Errorf("failed to watch pod: %w", err)
						}
					}
					if err := clientset.CoreV1().Pods(ns).Delete(context.Background(), pod.Name, metav1.DeleteOptions{}); err != nil {
						return fmt.Errorf("failed to delete pod: %w", err)
					}
				default:
					return fmt.Errorf("received unknown job type: %v", litter.Sdump(job))
				}
			}
		}
	}
}

func podFromJob(
	job api.CommandJob,
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
		default:
			env = append(env, corev1.EnvVar{Name: k, Value: v})
		}
	}
	volumeMounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	if len(pod.Spec.Containers) != 1 {
		return nil, fmt.Errorf("only one container is supported right now")
	}
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
	// agent server container
	agentContainer := corev1.Container{
		Name:            "wtf",
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
		Env: append(env, corev1.EnvVar{
			Name:  "BUILDKITE_AGENT_EXPERIMENT",
			Value: "kubernetes-exec",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_BOOTSTRAP_PHASES",
			Value: "checkout",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_AGENT_NAME",
			Value: "buildkite",
		}, corev1.EnvVar{
			Name:  "BUILDKITE_CONTAINER_ID",
			Value: "0",
		}),
	}
	checkoutContainer.Env = append(checkoutContainer.Env, env...)
	pod.Spec.Containers = append(pod.Spec.Containers, agentContainer, checkoutContainer)
	return pod, nil
}
