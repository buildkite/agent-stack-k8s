package scheduler

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
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
// container terminates while another regular container is still running. Once
// that deadline passes, Kubernetes fails the Job with DeadlineExceeded and
// deletes the pod, terminating all remaining containers.
//
// When every other regular container has already terminated, no deadline is
// set. That covers pods with no sidecars and pods using native Kubernetes
// sidecars (init containers with RestartPolicy: Always), which the kubelet
// terminates itself. A deadline would only risk failing a Job whose pod is
// about to succeed (#999).
type completionsWatcher struct {
	logger                        *slog.Logger
	k8s                           kubernetes.Interface
	terminationGracePeriodSeconds int64

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure goroutines are cleaned up.
	resourceEventHandlerCtx context.Context
}

func NewPodCompletionWatcher(logger *slog.Logger, k8s kubernetes.Interface, terminationGracePeriodSeconds int) *completionsWatcher {
	return &completionsWatcher{
		logger:                        logger,
		k8s:                           k8s,
		terminationGracePeriodSeconds: int64(terminationGracePeriodSeconds),
	}
}

// RegisterInformer creates a Pods informer and registers the handler on it.
func (w *completionsWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Core().V1().Pods().Informer()
	// Must be set before AddEventHandler: if the informer has already been
	// started, AddEventHandler replays cached Pods to the handler immediately,
	// on another goroutine. See note on field.
	w.resourceEventHandlerCtx = ctx
	if _, err := informer.AddEventHandler(w); err != nil {
		return err
	}
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
// If so, and another regular container is still running, it ensures the job is
// cleaned up by updating it with an ActiveDeadlineSeconds value.
//
// This is necessary for:
//  1. Cleaning up unmanaged containers added via podSpecPatch
//  2. Backward compatibility with legacy sidecars from older controller versions
func (w *completionsWatcher) cleanupSidecars(ctx context.Context, pod *corev1.Pod) {
	// Ignore completed (succeeded or failed) and unknown status pods.
	// Most of the time, the remaining logic applies to "Running" pods.
	// ("Pending" is dubious... one or more of the containers haven't started,
	// but the agent has run and terminated? still, it seems theoretically
	// possible.)
	switch pod.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed, corev1.PodUnknown:
		return
	}
	// Ignore pods where the agent hasn't terminated.
	terminated := getAgentTermination(pod)
	if terminated == nil {
		return
	}
	w.logger.Debug(
		"agent finished",
		"uuid", pod.Labels[config.UUIDLabel],
		"exit code", terminated.ExitCode,
	)

	// Normally the command containers have exited by the time the agent does,
	// so a regular container still running here is an unmanaged one. If there
	// are none, the pod finishes on its own (the kubelet stops any native
	// sidecars), and a deadline could only fail a Job whose pod is about to
	// succeed (#999).
	if !otherContainersRunning(pod) {
		return
	}

	// Measure from when the agent exited, so a replay after a controller
	// restart computes the same deadline.
	agentExit := terminated.FinishedAt.Time
	if agentExit.IsZero() {
		agentExit = time.Now()
	}

	var previous *int64
	var deadline int64
	var written bool
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		job, err := w.k8s.BatchV1().Jobs(pod.Namespace).Get(ctx, pod.Labels["job-name"], metav1.GetOptions{})
		if err != nil {
			return err
		}
		previous = job.Spec.ActiveDeadlineSeconds
		deadline = sidecarCleanupDeadline(job, w.terminationGracePeriodSeconds, agentExit)
		if previous != nil && *previous == deadline {
			return nil
		}
		job.Spec.ActiveDeadlineSeconds = new(deadline)
		if _, err = w.k8s.BatchV1().Jobs(pod.Namespace).Update(ctx, job, metav1.UpdateOptions{}); err != nil {
			return err
		}
		written = true
		return nil
	}); err != nil {
		completionWatcherJobCleanupErrorsCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()
		w.logger.Error("failed to update job with ActiveDeadlineSeconds", "error", err)
		return
	}
	if !written {
		w.logger.Debug("job ActiveDeadlineSeconds unchanged", "uuid", pod.Labels[config.UUIDLabel], "deadline", deadline)
		return
	}
	w.logger.Debug(
		"set job ActiveDeadlineSeconds for sidecar cleanup",
		"uuid", pod.Labels[config.UUIDLabel],
		"previous", optionalSeconds(previous),
		"deadline", deadline,
	)
	completionWatcherJobCleanupsCounter.Inc()
}

// optionalSeconds returns the value of an optional seconds field for logging,
// or nil when it is unset (rather than the pointer itself).
func optionalSeconds(seconds *int64) any {
	if seconds == nil {
		return nil
	}
	return *seconds
}

// sidecarCleanupBufferSeconds is the least time the remaining containers get
// after the agent exits before the Job's deadline is reached, for a job that
// has already run longer than the grace period. It is capped at the grace
// period.
//
// It only needs to cover a command container that is still finishing, or whose
// terminated status hasn't been reported yet, when the agent exits: those exit
// within seconds. An unmanaged container that never exits on its own gets no
// signal during this time, so a longer buffer would only keep its Job (and its
// max-in-flight slot) active longer. Once the deadline passes, deleting the pod
// still gives it the pod's terminationGracePeriodSeconds to stop.
const sidecarCleanupBufferSeconds = 10

// otherContainersRunning reports whether any regular container other than the
// agent has not terminated yet. Containers that haven't started (Waiting) count
// as not terminated, so a pod stuck on an unmanaged container is cleaned up too.
func otherContainersRunning(pod *corev1.Pod) bool {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != AgentContainerName && container.State.Terminated == nil {
			return true
		}
	}
	return false
}

// sidecarCleanupDeadline returns the ActiveDeadlineSeconds value for cleaning
// up the containers still running when the agent exited at agentExit.
//
// ActiveDeadlineSeconds is measured from the Job's status.startTime, not from
// when it is set. The deadline is the grace period from the Job's start, as it
// has always been, so a job that finished well within the grace period still
// gives its remaining containers the rest of it. For a job that ran longer, that
// deadline is already in the past: Kubernetes would fail the Job with
// DeadlineExceeded and start deleting the pod straight away, even when its
// remaining containers were about to exit cleanly (#999). So the deadline is
// never earlier than sidecarCleanupBufferSeconds (capped at the grace period)
// after the agent exited.
//
// Clock skew between the node (agentExit) and the control plane (startTime, and
// the deadline check) shortens or lengthens that buffer by the same amount.
//
// An existing deadline that is already sooner is kept, so cleanup never extends
// a job's configured lifetime.
func sidecarCleanupDeadline(job *batchv1.Job, gracePeriodSeconds int64, agentExit time.Time) int64 {
	deadline := gracePeriodSeconds
	if start := job.Status.StartTime; start != nil {
		// When the agent exited at or before the Job's start (clock skew),
		// elapsed+buffer is at most the grace period, so max keeps the grace
		// period.
		elapsed := int64(math.Ceil(agentExit.Sub(start.Time).Seconds()))
		buffer := min(gracePeriodSeconds, sidecarCleanupBufferSeconds)
		deadline = max(deadline, elapsed+buffer)
	}
	if current := job.Spec.ActiveDeadlineSeconds; current != nil && *current < deadline {
		return *current
	}
	return deadline
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
