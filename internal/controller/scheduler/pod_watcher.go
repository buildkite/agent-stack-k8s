package scheduler

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent/v3/agent"
	"github.com/buildkite/roko"

	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
)

type podWatcher struct {
	logger      *zap.Logger
	k8s         kubernetes.Interface
	agentClient *api.AgentClient
	cfg         *config.Config

	// ImagePullBackOff detection waits at least this duration after pod
	// creation before it cancels the job.
	imagePullBackOffGracePeriod time.Duration

	// Jobs that we've failed, cancelled, or were found to be in a terminal
	// state.
	ignoredJobsMu sync.RWMutex
	ignoredJobs   map[uuid.UUID]struct{}

	// Pods being watched for image-related failures (ImagePullBackOff,
	// ErrImageNeverPull, etc)
	watchingForImageFailureMu sync.Mutex
	watchingForImageFailure   map[uuid.UUID]*corev1.Pod

	// Buildkite job checker for handling job cancellation
	bkJobChecker *BkJobChecker

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure job cancel checkers are cleaned up.
	resourceEventHandlerCtx context.Context
}

// NewPodWatcher creates an informer that does various things with pods and
// Buildkite jobs:
//   - If an init container fails, the BK Agent REST API will be used to fail
//     the job (since an agent hasn't run yet).
//   - If a container stays in ImagePullBackOff or ErrImageNeverPull state for
//     too long, the BK Agent REST API will be used to fail the job and the pod
//     will be evicted.
//   - If a container stays in ImagePullBackOff or ErrImageNeverPull, and the
//     pod somehow got through all the init containers (including the image
//     checks...) the Agent container should eventually detect a "lost client"
//     and fail the job.
//   - If a pod is pending, every so often Buildkite will be checked to see if
//     the corresponding job has been cancelled so that the pod can be evicted
//     early.
func NewPodWatcher(logger *zap.Logger, k8s kubernetes.Interface, agentClient *api.AgentClient, cfg *config.Config) *podWatcher {
	imagePullBackOffGracePeriod := cfg.ImagePullBackOffGracePeriod
	if imagePullBackOffGracePeriod <= 0 {
		imagePullBackOffGracePeriod = config.DefaultImagePullBackOffGracePeriod
	}
	jobCancelCheckerInterval := cfg.JobCancelCheckerPollInterval
	if jobCancelCheckerInterval <= 0 {
		jobCancelCheckerInterval = config.DefaultJobCancelCheckerPollInterval
	}

	pw := &podWatcher{
		logger:                      logger,
		k8s:                         k8s,
		agentClient:                 agentClient,
		cfg:                         cfg,
		imagePullBackOffGracePeriod: imagePullBackOffGracePeriod,
		ignoredJobs:                 make(map[uuid.UUID]struct{}),
		watchingForImageFailure:     make(map[uuid.UUID]*corev1.Pod),
		bkJobChecker:                NewBkJobChecker(logger, agentClient, k8s, jobCancelCheckerInterval),
	}
	podWatcherIgnoredJobsGaugeFunc = func() int {
		pw.ignoredJobsMu.RLock()
		defer pw.ignoredJobsMu.RUnlock()
		return len(pw.ignoredJobs)
	}
	jobCancelCheckerGaugeFunc = func() int {
		return pw.bkJobChecker.GetActiveCheckCount()
	}
	watchingForImageFailureGaugeFunc = func() int {
		pw.watchingForImageFailureMu.Lock()
		defer pw.watchingForImageFailureMu.Unlock()
		return len(pw.watchingForImageFailure)
	}
	return pw
}

// Creates a Pods informer and registers the handler on it
func (w *podWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return err
	}
	w.resourceEventHandlerCtx = ctx // 😡
	go factory.Start(ctx.Done())
	go w.imageFailureChecker(ctx, w.logger)
	return nil
}

func (w *podWatcher) OnDelete(previousState any) {
	podWatcherOnDeleteEventCounter.Inc()

	pod, _ := previousState.(*corev1.Pod)
	if pod == nil {
		return
	}

	log := loggerForObject(w.logger, pod)
	jobUUID, err := jobUUIDForObject(pod)
	if err != nil {
		log.Error("Job UUID label missing or invalid for pod")
		return
	}

	// No need to continue watching for image-related failures or cancellation.
	w.stopWatchingForImageFailure(jobUUID)
	w.bkJobChecker.StopChecking(jobUUID)

	// The pod is gone, so we can stop ignoring it (if it comes back).
	w.unignoreJob(jobUUID)
}

func (w *podWatcher) OnAdd(currentState any, _ bool) {
	podWatcherOnAddEventCounter.Inc()

	pod, _ := currentState.(*corev1.Pod)
	if pod == nil {
		return
	}

	w.runChecks(w.resourceEventHandlerCtx, pod)
}

func (w *podWatcher) OnUpdate(_, currentState any) {
	podWatcherOnUpdateEventCounter.Inc()

	// The previous state of the objects is not needed to run checks.
	pod, _ := currentState.(*corev1.Pod)
	if pod == nil {
		return
	}
	w.runChecks(w.resourceEventHandlerCtx, pod)
}

func (w *podWatcher) runChecks(ctx context.Context, pod *corev1.Pod) {
	log := loggerForObject(w.logger, pod)
	jobUUID, err := jobUUIDForObject(pod)
	if err != nil {
		log.Error("Job UUID label missing or invalid for pod")
		return
	}

	// If it's no longer pending, stop the job cancel checker. If its neither
	// pending nor running, stop watching for image-related failures.
	// (This makes sense to do whether or not the job UUID is ignored.)
	switch pod.Status.Phase {
	case corev1.PodPending:
		// Continue watching for failures or cancellation, if already doing so.

	case corev1.PodRunning:
		// Running: the agent container has started or is about to start, and it
		//          can handle the cancellation and exit.
		w.bkJobChecker.StopChecking(jobUUID)

	default:
		// Succeeded, Failed: it's already over.
		// Unknown: probably shouldn't interfere.
		w.stopWatchingForImageFailure(jobUUID)
		w.bkJobChecker.StopChecking(jobUUID)
	}

	if w.isIgnored(jobUUID) {
		log.Debug("Job is currently ignored for podWatcher checks")
		return
	}

	// Check for an init container that failed for any reason.
	// (Note: users can define their own init containers through podSpec.)
	w.failOnInitContainerFailure(ctx, log, pod)

	// Check for Buildkite job cancellation while the pod is pending.
	// Check that the pod doesn't stay in ImagePullBackOff or ErrImageNeverPull
	// for too long.
	switch pod.Status.Phase {
	case corev1.PodPending:
		w.watchForImageFailure(jobUUID, pod)
		w.bkJobChecker.StartChecking(ctx, log, pod.ObjectMeta, jobUUID)

	case corev1.PodRunning:
		w.watchForImageFailure(jobUUID, pod)
	}
}

// podHasFailingImages returns a slice of container statuses when a pod has
// a container in an image-related failing state (ImagePullBackOff,
// ErrImageNeverPull, etc) for too long. If the slice is empty or nil, the pod
// is not failing (yet).
func (w *podWatcher) podHasFailingImages(log *zap.Logger, pod *corev1.Pod) []corev1.ContainerStatus {
	failImmediately := false // becomes true for InvalidImageName

	var statuses []corev1.ContainerStatus

	// If any init container fails to pull, whether it's one we added
	// specifically to check for pull failure, the pod won't run.
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		waiting := containerStatus.State.Waiting
		if waiting == nil {
			continue
		}

		switch waiting.Reason {
		case "ImagePullBackOff", "ErrImageNeverPull":
			statuses = append(statuses, containerStatus)

		case "InvalidImageName":
			statuses = append(statuses, containerStatus)
			failImmediately = true
		}
	}

	// These containers only run after the init containers have run.
	// Theoretically this could still happen even if all the init containers
	// successfully pulled.
	for _, containerStatus := range pod.Status.ContainerStatuses {
		waiting := containerStatus.State.Waiting
		if waiting == nil {
			continue
		}

		switch waiting.Reason {
		case "ImagePullBackOff", "ErrImageNeverPull":
			if !isSystemContainer(&containerStatus) {
				log.Info("Ignoring container during ImagePullBackOff watch.", zap.String("name", containerStatus.Name))
				continue
			}
			statuses = append(statuses, containerStatus)

		case "InvalidImageName":
			statuses = append(statuses, containerStatus)
			failImmediately = true
		}
	}

	if len(statuses) == 0 {
		// All's well with the world.
		return nil
	}

	if failImmediately {
		return statuses
	}

	// Apply the grace period
	if pod.Status.StartTime == nil {
		// Status could be unpopulated, or it hasn't started yet.
		return nil
	}
	startedAt := pod.Status.StartTime.Time
	if startedAt.IsZero() || time.Since(startedAt) < w.imagePullBackOffGracePeriod {
		// Not started yet, or started recently
		return nil
	}

	return statuses
}

// failOnInitContainerFailure looks for init containers that failed, and fails
// the job on Buildkite.
func (w *podWatcher) failOnInitContainerFailure(ctx context.Context, log *zap.Logger, pod *corev1.Pod) {
	log.Debug("Checking pod for failed init containers")

	containerFails := make(map[string]*corev1.ContainerStateTerminated)
	var lastFailExitCode int32

	// If any init container fails, whether it's one we added specifically to
	// check for pull failure or not, the pod won't run.
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		term := containerStatus.State.Terminated
		if term == nil || term.ExitCode == 0 { // not terminated, or succeeded
			continue
		}
		containerFails[containerStatus.Name] = term
		lastFailExitCode = term.ExitCode
	}

	if len(containerFails) == 0 {
		// All's well with the world.
		return
	}

	// Attempt to acquire it and fail it ourselves.
	// Don't bother checking the current BK state of the job in advance, since
	// it should always be api.JobStatesScheduled. Init containers must all
	// succeed before the agent container starts.
	// If it's not in Scheduled state, acquire will fail, but also that would
	// imply something weird is going on with the job (another agent?) and we
	// probably shouldn't interfere.
	log.Info("One or more init containers failed. Failing.")
	message := w.formatInitContainerFails(containerFails)
	failureInfo := FailureInfo{
		Message:  message,
		ExitCode: lastFailExitCode,
		Reason:   agent.SignalReasonStackError,
	}
	if err := acquireAndFailForObject(ctx, log, w.k8s, w.cfg, pod, failureInfo); err != nil {
		// Maybe the job was cancelled in the meantime?
		log.Error("Could not fail Buildkite job", zap.Error(err))
		podWatcherBuildkiteJobFailErrorsCounter.Inc()
		return
	}
	podWatcherBuildkiteJobFailsCounter.Inc()
	// No need to fall back to cancelling if acquire failed - see above.
	// No need to evict, the pod should be considered failed already.
}

func (w *podWatcher) formatInitContainerFails(terms map[string]*corev1.ContainerStateTerminated) string {
	keys := make([]string, 0, len(terms))
	for k := range terms {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	tw := table.NewWriter()
	tw.SetStyle(table.StyleColoredDark)
	tw.AppendHeader(table.Row{"CONTAINER", "EXIT CODE", "SIGNAL", "REASON", "MESSAGE"})
	tw.AppendSeparator()
	for _, key := range keys {
		term := terms[key]
		tw.AppendRow(table.Row{key, term.ExitCode, term.Signal, term.Reason, term.Message})
	}
	return "The following init containers failed:\n\n" + tw.Render()
}

func (w *podWatcher) formatImagePullFailureMessage(statuses []corev1.ContainerStatus) string {
	slices.SortFunc(statuses, func(a, b corev1.ContainerStatus) int {
		return cmp.Compare(a.Name, b.Name)
	})

	tw := table.NewWriter()
	tw.SetStyle(table.StyleColoredDark)
	tw.AppendHeader(table.Row{"CONTAINER", "IMAGE REF", "REASON", "MESSAGE"})
	tw.AppendSeparator()
	for _, status := range statuses {
		// So far this function is only used for Waiting statuses.
		tw.AppendRow(table.Row{
			status.Name,
			strconv.Quote(status.Image), // may be malformed, hence quoting
			status.State.Waiting.Reason,
			status.State.Waiting.Message,
		})
	}
	return "The following images could not be pulled or were unavailable:\n\n" + tw.Render()
}

func forcefullyDeletePod(
	ctx context.Context,
	log *zap.Logger,
	k8s kubernetes.Interface,
	podMetadata *metav1.ObjectMeta,
	reason string,
) error {
	// Force immediate deletion with zero grace period
	// We generally believe when our controller decide a payload should go, there shouldn't be good reason to keep it waiting for
	// graceful timeout.
	//
	// Some organizations conduct massive job cancelling, without this, it will cause unnecessary cluster resource saturation.
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: new(int64), // zero value
	}

	if err := k8s.CoreV1().Pods(podMetadata.Namespace).Delete(ctx, podMetadata.Name, deleteOptions); err != nil {
		log.Error("Couldn't forcefully delete pod", zap.Error(err))
		forcefulPodDeletionErrorsCounter.WithLabelValues(reason, string(kerrors.ReasonForError(err))).Inc()
		return err
	}
	forcefullyDeletedPodCounter.WithLabelValues(reason).Inc()

	return nil
}

// imageFailureChecker is a goroutine that periodically checks pending and
// running pods for container statuses such as ImagePullBackOff,
// ErrImageNeverPull, etc.
func (w *podWatcher) imageFailureChecker(ctx context.Context, log *zap.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			// continue below
		}

		var failingPods []failingPod

		// Move failing pods from pendingPods to failingPods.
		w.watchingForImageFailureMu.Lock()
		for jobUUID, pod := range w.watchingForImageFailure {
			statuses := w.podHasFailingImages(log, pod)
			if len(statuses) == 0 {
				continue
			}
			failingPods = append(failingPods, failingPod{jobUUID: jobUUID, pod: pod, statuses: statuses})
			delete(w.watchingForImageFailure, jobUUID)
		}
		w.watchingForImageFailureMu.Unlock()

		// Fail/cancel the corresponding jobs on Buildkite, and evict the
		// pods.
		for _, fp := range failingPods {
			w.failForImageFailure(ctx, log, fp)
		}
	}
}

// failingPod captures information about a pending or running pod that is now
// failing.
type failingPod struct {
	jobUUID  uuid.UUID
	pod      *corev1.Pod
	statuses []corev1.ContainerStatus
}

// failForImageFailure fails or cancels the corresponding job on Buildkite, and
// evicts the pod as needed.
func (w *podWatcher) failForImageFailure(ctx context.Context, log *zap.Logger, failingPod failingPod) {
	jobUUID := failingPod.jobUUID
	pod := failingPod.pod
	statuses := failingPod.statuses

	// Get the current job state from BK.
	// What we do next depends on what state it is in.
	retrier := roko.NewRetrier(
		roko.WithStrategy(roko.ExponentialSubsecond(1*time.Second)),
		roko.WithJitterRange(-1*time.Second, 1*time.Second),
		roko.WithMaxAttempts(5),
	)
	job, err := roko.DoFunc(ctx, retrier, func(*roko.Retrier) (*api.AgentJobState, error) {
		job, retryAfter, err := w.agentClient.GetJobState(ctx, jobUUID.String())
		if api.IsPermanentError(err) {
			retrier.Break()
		}
		retrier.SetNextInterval(max(retryAfter, retrier.NextInterval()))
		return job, err
	})
	if err != nil {
		log.Warn("Failed to fetch state of job", zap.Error(err))
		return
	}
	log = log.With(zap.String("job_state", string(job.State)))

	switch job.State {
	case api.JobStateScheduled, api.JobStateReserved:
		// We can acquire it and fail it ourselves.
		// Note with the reserved state, we are assuming that the current stack runtime would be the reservation owner.
		log.Info("One or more job containers are waiting too long for images. Failing.")
		message := w.formatImagePullFailureMessage(statuses)
		failureInfo := FailureInfo{
			Message: message,
			// Do we have a better status code to report here?
		}
		if err := acquireAndFailForObject(ctx, log, w.k8s, w.cfg, pod, failureInfo); err != nil {
			podWatcherBuildkiteJobFailErrorsCounter.Inc()
			// Maybe the job was acquired by an agent in the meantime?
			// Maybe the job was cancelled in the meantime?
			// Either way we've done what we can here.
			log.Error("Could not fail Buildkite job", zap.Error(err))
			return
		}
		podWatcherBuildkiteJobFailsCounter.Inc()
		// Also delete the pod, because it won't die on its own.

		if err := forcefullyDeletePod(ctx, log, w.k8s, &pod.ObjectMeta, "image_pull_failure"); err != nil {
			// K8s API overloaded? rate limit? -> TODO in this case we should retry
			// Admission controller blocks this?
			// Insufficient RBAC permission?
			// K8s node goes down?
			// In these case, we've done what we can.
			return
		}

		// Mark the job as ignored to avoid further processing
		jobUUID, err := jobUUIDForObject(&pod.ObjectMeta)
		if err != nil {
			log.Error("Could find Job UUID from pod metadata", zap.Error(err))
			return
		}
		w.ignoreJob(jobUUID)

	case api.JobStateAccepted, api.JobStateAssigned, api.JobStateRunning:
		// An agent is already doing something with the job. Let it fail.
		log.Debug("Job is the responsibility of an agent")

	case api.JobStateCanceling, api.JobStateCanceled, api.JobStateFinished, api.JobStateSkipped:
		// If the job is in one of these states, we can neither acquire nor
		// cancel it (now or in the future).
		log.Debug("Job not acquirable or cancelable")
		w.ignoreJob(jobUUID)

	default:
		// Most states don't make sense for a command job that we've started
		// a pod for (e.g. blocked, broken, expired, pending, waiting, ...)
		// Maybe the meanings of states has changed since this build?
		// Log a message but don't do anything.
		log.Warn("Job not in actionable state")
	}
}

func (w *podWatcher) ignoreJob(jobUUID uuid.UUID) {
	w.ignoredJobsMu.Lock()
	defer w.ignoredJobsMu.Unlock()
	w.ignoredJobs[jobUUID] = struct{}{}
}

func (w *podWatcher) unignoreJob(jobUUID uuid.UUID) {
	w.ignoredJobsMu.Lock()
	defer w.ignoredJobsMu.Unlock()
	delete(w.ignoredJobs, jobUUID)
}

func (w *podWatcher) isIgnored(jobUUID uuid.UUID) bool {
	w.ignoredJobsMu.RLock()
	defer w.ignoredJobsMu.RUnlock()
	_, ignore := w.ignoredJobs[jobUUID]
	return ignore
}

func (w *podWatcher) watchForImageFailure(jobUUID uuid.UUID, pod *corev1.Pod) {
	w.watchingForImageFailureMu.Lock()
	defer w.watchingForImageFailureMu.Unlock()
	w.watchingForImageFailure[jobUUID] = pod
}

func (w *podWatcher) stopWatchingForImageFailure(jobUUID uuid.UUID) {
	w.watchingForImageFailureMu.Lock()
	defer w.watchingForImageFailureMu.Unlock()
	delete(w.watchingForImageFailure, jobUUID)
}

// All container-\d containers will have the agent installed as their PID 1.
// Therefore, their lifecycle is well monitored in our backend, allowing us to terminate them if they fail to start.
//
// However, sidecar containers are completely unmonitored.
// We avoid terminating jobs due to sidecar image pull backoff watcher
// to prevent customer confusion.
//
// Most importantly, the CI can still pass (in theory) even if sidecars fail.
//
// (The name "system container" is subject to more debate.)
func isSystemContainer(containerStatus *corev1.ContainerStatus) bool {
	name := containerStatus.Name
	if slices.Contains([]string{AgentContainerName, CopyAgentContainerName, CheckoutContainerName}, name) {
		return true
	}
	// This will arguably cause some false positives, but:
	//   1. The change is low.
	//   2. we plan replace this soon.
	matched, _ := regexp.MatchString(`container-\d+`, name)
	return matched
}
