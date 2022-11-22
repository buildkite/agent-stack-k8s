package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/sanity-io/litter"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
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
		Containers: []corev1.Container{
			{
				Name:  "agent",
				Image: "buildkite/agent:latest",
			},
		},
	},
}

const ns = "default"

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
							switch pod.Status.Phase {
							case corev1.PodPending, corev1.PodRunning, corev1.PodUnknown:
								log.Println(pod.Status.Phase)
								return false, nil
							case corev1.PodSucceeded:
								log.Println("pod success!")
								return true, nil
							case corev1.PodFailed:
								log.Println(pod.Status.Phase)
								return false, fmt.Errorf("pod failed")
							default:
								return false, fmt.Errorf("unexpected pod status: %s", pod.Status.Phase)
							}
						}
						return false, errors.New("event object not of type v1.Node")
					})
					if err != nil {
						return fmt.Errorf("failed to watch pod: %w", err)
					}
					if err := clientset.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
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
	var pod *corev1.Pod
	envMap := map[string]string{}
	for _, val := range job.Env {
		parts := strings.Split(val, "=")
		envMap[parts[0]] = parts[1]
	}
	pod = defaultBootstrapPod.DeepCopy()
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "workspace",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
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
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	volumeMounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	massageContainers := func(name string, containers []corev1.Container, c corev1.Container, i int) {
		c.Env = append(c.Env, env...)
		if c.Name == "" {
			c.Name = fmt.Sprintf("%s-%d", name, i)
		}
		if c.WorkingDir == "" {
			c.WorkingDir = "/workspace"
		}
		c.VolumeMounts = append(c.VolumeMounts, volumeMounts...)
		containers[i] = c
	}
	for i, c := range pod.Spec.Containers {
		massageContainers("container", pod.Spec.Containers, c, i)
	}
	for i, c := range pod.Spec.InitContainers {
		massageContainers("init", pod.Spec.InitContainers, c, i)
	}
	return pod, nil
}
