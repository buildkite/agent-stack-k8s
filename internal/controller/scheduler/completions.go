package scheduler

import (
	"context"
	"log/slog"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

// completionsWatcher watches for pods where the agent container has terminated
// and cleans up the associated job by setting ActiveDeadlineSeconds.
//
// This component handles cleanup for unmanaged containers that would otherwise
// continue running after the agent terminates, preventing pod cleanup. Unmanaged
// containers are those not coordinated by the buildkite-agent, specifically:
//
//  1. Containers added via podSpecPatch that are new (not patching an existing
//     container) - these are treated as unmanaged sidecars and their commands
//     are not substituted with the buildkite-agent bootstrap process.
//
//  2. Legacy sidecars from older controller versions. Prior to native Kubernetes
//     sidecars (KEP-753), "sidecars" were added to podSpec.Containers as regular
//     containers. When upgrading from versions using the legacy approach (e.g.,
//     v0.34.0) to versions using native sidecars (e.g., v0.36.0+), in-flight jobs
//     would be orphaned without this cleanup logic.
//
// Note: Containers defined directly in podSpec.Containers (via the kubernetes
// plugin's podSpec field) are NOT unmanaged - they are transformed into command
// containers with their entrypoints substituted for buildkite-agent bootstrap.
//
// The cleanup works by setting ActiveDeadlineSeconds on the job when the agent
// container terminates, which causes Kubernetes to terminate all remaining
// containers in the pod.
//
// For pods using native Kubernetes sidecars (init containers with RestartPolicy:
// Always), this cleanup is redundant but harmless as Kubernetes already handles
// their termination automatically.
type completionsWatcher struct {
	logger *slog.Logger
	k8s    kubernetes.Interface

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure goroutines are cleaned up.
	resourceEventHandlerCtx context.Context
}

func NewPodCompletionWatcher(logger *slog.Logger, k8s kubernetes.Interface) *completionsWatcher {
	return &completionsWatcher{
		logger: logger,
		k8s:    k8s,
	}
}

// RegisterInformer creates a Pods informer and registers the handler on it.
func (w *completionsWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return err
	}
	w.resourceEventHandlerCtx = ctx // see note on field
	go factory.Start(ctx.Done())
	return nil
}

// OnDelete is ignored - we don't need to handle pod deletions.
func (w *completionsWatcher) OnDelete(obj any) {}

// OnAdd handles pods that completed while the controller wasn't running.
func (w *completionsWatcher) OnAdd(obj any, isInInitialList bool) {
	completionWatcherOnAddEventCounter.Inc()
	pod := obj.(*corev1.Pod)
	w.cleanupSidecars(w.resourceEventHandlerCtx, pod)
}

// OnUpdate handles pods where the agent container just terminated.
func (w *completionsWatcher) OnUpdate(old any, new any) {
	completionWatcherOnUpdateEventCounter.Inc()

	oldPod := old.(*corev1.Pod)
	if terminated := getAgentTermination(oldPod); terminated != nil {
		// skip subsequent reconciles after we've already handled termination
		return
	}

	newPod := new.(*corev1.Pod)
	w.cleanupSidecars(w.resourceEventHandlerCtx, newPod)
}

// cleanupSidecars checks if the agent container in the pod has terminated.
// If so, it ensures the job is cleaned up by updating it with an
// ActiveDeadlineSeconds value.
//
// This is necessary for:
//  1. Cleaning up unmanaged containers added via podSpecPatch
//  2. Backward compatibility with legacy sidecars from older controller versions
//
// For pods using native Kubernetes sidecars (init containers with RestartPolicy:
// Always), this cleanup is redundant but harmless as Kubernetes already handles
// their termination automatically.
func (w *completionsWatcher) cleanupSidecars(ctx context.Context, pod *corev1.Pod) {
	terminated := getAgentTermination(pod)
	if terminated == nil {
		return
	}
	w.logger.Debug(
		"agent finished",
		"uuid", pod.Labels[config.UUIDLabel],
		"exit code", terminated.ExitCode,
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
		w.logger.Error("failed to update job with ActiveDeadlineSeconds", "error", err)
		return
	}
	completionWatcherJobCleanupsCounter.Inc()
}

// getAgentTermination returns the termination state of the agent container
// if it has terminated, or nil otherwise.
func getAgentTermination(pod *corev1.Pod) *corev1.ContainerStateTerminated {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != AgentContainerName {
			continue
		}
		if container.State.Terminated != nil {
			return container.State.Terminated
		}
	}
	return nil
}
