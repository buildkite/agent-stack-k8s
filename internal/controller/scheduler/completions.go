package scheduler

import (
	"context"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	"go.uber.org/zap"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

type completionsWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure goroutines are cleaned up.
	resourceEventHandlerCtx context.Context
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
		return err
	}
	w.resourceEventHandlerCtx = ctx // see note on field
	go factory.Start(ctx.Done())
	return nil
}

// ignored
func (w *completionsWatcher) OnDelete(obj any) {}

// handle pods completed while the controller wasn't running
func (w *completionsWatcher) OnAdd(obj any, isInInitialList bool) {
	completionWatcherOnAddEventCounter.Inc()
	pod := obj.(*v1.Pod)
	w.cleanupSidecars(w.resourceEventHandlerCtx, pod)
}

func (w *completionsWatcher) OnUpdate(old any, new any) {
	completionWatcherOnUpdateEventCounter.Inc()

	oldPod := old.(*v1.Pod)
	if terminated := getTermination(oldPod); terminated != nil {
		// skip subsequent reconciles after we've already handled termination
		return
	}

	newPod := new.(*v1.Pod)
	w.cleanupSidecars(w.resourceEventHandlerCtx, newPod)
}

// cleanupSidecars first checks if the container status of the agent container
// in the pod is Terminated. If so, it ensures the job is cleaned up by updating
// it with an ActiveDeadlineSeconds value (defaultTermGracePeriodSeconds).
// (So this is not actually sidecar-specific, but is needed because sidecars
// would otherwise cause the pod to continue running.)
func (w *completionsWatcher) cleanupSidecars(ctx context.Context, pod *v1.Pod) {
	terminated := getTermination(pod)
	if terminated == nil {
		return
	}
	w.logger.Debug(
		"agent finished",
		zap.String("uuid", pod.Labels[config.UUIDLabel]),
		zap.Int32("exit code", terminated.ExitCode),
	)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		job, err := w.k8s.BatchV1().Jobs(pod.Namespace).Get(ctx, pod.Labels["job-name"], metav1.GetOptions{})
		if err != nil {
			return err
		}
		job.Spec.ActiveDeadlineSeconds = ptr.To[int64](config.DefaultTerminationGracePeriodSeconds)
		_, err = w.k8s.BatchV1().Jobs(pod.Namespace).Update(ctx, job, metav1.UpdateOptions{})
		return err
	}); err != nil {
		completionWatcherJobCleanupErrorsCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()
		w.logger.Error("failed to update job with ActiveDeadlineSeconds", zap.Error(err))
		return
	}
	completionWatcherJobCleanupsCounter.Inc()
}

func getTermination(pod *v1.Pod) *v1.ContainerStateTerminated {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != AgentContainerName {
			continue
		}
		if container.State.Terminated != nil {
			// oldPod is not terminated, but newPod is
			return container.State.Terminated
		}
	}
	return nil
}
