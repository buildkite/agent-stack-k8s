package scheduler

import (
	"context"
	"fmt"

	"github.com/buildkite/agent-stack-k8s/api"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
)

type completionsWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface
}

func NewPodCompletionWatcher(logger *zap.Logger, k8s kubernetes.Interface) *completionsWatcher {
	watcher := &completionsWatcher{
		logger: logger,
		k8s:    k8s,
	}
	return watcher
}

// Creates a Pods informer and registers the handler on it
func (w *completionsWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return fmt.Errorf("failed to register pod event handler: %w", err)
	}
	go factory.Start(ctx.Done())
	return nil
}

// ignored
func (w *completionsWatcher) OnDelete(obj interface{}) {}

// handle pods completed while the controller wasn't running
func (w *completionsWatcher) OnAdd(obj interface{}) {
	pod := obj.(*v1.Pod)
	w.cleanupSidecars(pod)
}

func (w *completionsWatcher) OnUpdate(old interface{}, new interface{}) {
	oldPod := old.(*v1.Pod)
	if terminated := getTermination(oldPod); terminated != nil {
		// skip subsequent reconciles after we've already handled termination
		return
	}

	newPod := new.(*v1.Pod)
	w.cleanupSidecars(newPod)
}

func (w *completionsWatcher) cleanupSidecars(pod *v1.Pod) {
	if terminated := getTermination(pod); terminated != nil {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			job, err := w.k8s.BatchV1().Jobs(pod.Namespace).Get(context.TODO(), pod.Labels["job-name"], metav1.GetOptions{})
			if err != nil {
				return err
			} else {
				job.Spec.ActiveDeadlineSeconds = pointer.Int64(1)
				_, err = w.k8s.BatchV1().Jobs(pod.Namespace).Update(context.TODO(), job, metav1.UpdateOptions{})
				return err
			}
		}); err != nil {
			w.logger.Error("failed to update job", zap.Error(err))
		}
		w.logger.Debug("agent finished", zap.String("uuid", pod.Labels[api.UUIDLabel]), zap.Int32("exit code", terminated.ExitCode))
	}
}

func getTermination(pod *v1.Pod) *v1.ContainerStateTerminated {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == AgentContainerName {
			if container.State.Terminated != nil {
				// oldPod is not terminated, but newPod is
				return container.State.Terminated
			}
		}
	}
	return nil
}
