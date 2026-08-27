package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/buildkite/agent/v3/agent"

	"log/slog"

	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// jobWatcher watches k8s jobs for failure to start a pod. The corresponding
// Buildkite job is failed with an error message if this happens. Also, if such
// a k8s job doesn't enter a terminal state on its own, jobWatcher sets a
// deadline so that it is cleaned up.
type jobWatcher struct {
	// Logs go here
	logger *slog.Logger

	k8s kubernetes.Interface
	cfg *config.Config

	agentClient *api.AgentClient

	// Shared with podWatcher. A k8s Job held suspended by an admission
	// controller never produces a pod, so pod events alone never register it
	// for cancellation checks.
	bkJobChecker *BatchBuildkiteJobChecker

	// Tracks stalling jobs (jobs that have yet to create pods).
	stallingJobsMu sync.Mutex
	stallingJobs   map[uuid.UUID]*batchv1.Job

	// Tracks jobs that are being cleaned up (to avoid repeats).
	ignoredJobsMu sync.RWMutex
	ignoredJobs   map[uuid.UUID]struct{}

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure goroutines are cleaned up.
	resourceEventHandlerCtx context.Context
}

// NewJobWatcher creates a JobWatcher.
func NewJobWatcher(logger *slog.Logger, k8sClient kubernetes.Interface, agentClient *api.AgentClient, cfg *config.Config, bkJobChecker *BatchBuildkiteJobChecker) *jobWatcher {
	w := &jobWatcher{
		logger:       logger,
		k8s:          k8sClient,
		agentClient:  agentClient,
		cfg:          cfg,
		bkJobChecker: bkJobChecker,
		stallingJobs: make(map[uuid.UUID]*batchv1.Job),
		ignoredJobs:  make(map[uuid.UUID]struct{}),
	}
	jobsStallingGaugeFunc = func() int {
		w.stallingJobsMu.Lock()
		defer w.stallingJobsMu.Unlock()
		return len(w.stallingJobs)
	}
	jobWatcherIgnoredJobsGaugeFunc = func() int {
		w.ignoredJobsMu.Lock()
		defer w.ignoredJobsMu.Unlock()
		return len(w.ignoredJobs)
	}
	return w
}

// RegisterInformer registers the limiter to listen for Kubernetes job events.
func (w *jobWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	if _, err := jobInformer.AddEventHandler(w); err != nil {
		return err
	}
	w.resourceEventHandlerCtx = ctx // See field comment
	go factory.Start(ctx.Done())
	// No need to wait for cache sync here. These are cleanup tasks, not
	// barriers to prevent creating new jobs.
	go w.stalledJobChecker(ctx)
	return nil
}

// OnAdd is called by k8s to inform us a resource is added.
func (w *jobWatcher) OnAdd(obj any, _ bool) {
	jobWatcherOnAddEventCounter.Inc()
	kjob, _ := obj.(*batchv1.Job)
	if kjob == nil {
		return
	}
	// Same logic whether we are considering pre-existing jobs, or new jobs.
	w.runChecks(w.resourceEventHandlerCtx, kjob)
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (w *jobWatcher) OnUpdate(_, curr any) {
	jobWatcherOnUpdateEventCounter.Inc()
	kjob, _ := curr.(*batchv1.Job)
	if kjob == nil {
		return
	}
	// Same logic whether or not anything relevant changed about the job.
	w.runChecks(w.resourceEventHandlerCtx, kjob)
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (w *jobWatcher) OnDelete(prev any) {
	jobWatcherOnDeleteEventCounter.Inc()
	kjob, _ := prev.(*batchv1.Job)
	if kjob == nil {
		return
	}
	w.cleanupJobAcquisitionTokenSecret(w.resourceEventHandlerCtx, loggerForObject(w.logger, kjob), kjob)

	jobUUID, err := jobUUIDForObject(kjob)
	if err != nil {
		return
	}

	w.removeFromStalling(jobUUID)
	w.stopCheckingBuildkiteJob(jobUUID)

	// The job is gone, so we can stop ignoring it (if it comes back).
	w.unignoreJob(jobUUID)

	// TODO: consider catching jobs that were deleted manually?
}

func (w *jobWatcher) runChecks(ctx context.Context, kjob *batchv1.Job) {
	log := loggerForObject(w.logger, kjob)
	jobUUID, err := jobUUIDForObject(kjob)
	if err != nil {
		log.Error("Job UUID label missing or invalid for job")
		return
	}

	if w.isIgnored(jobUUID) {
		log.Debug("Job is currently ignored for jobWatcher checks")
		return
	}

	if model.JobFinished(kjob) {
		w.removeFromStalling(jobUUID)
		w.stopCheckingBuildkiteJob(jobUUID)
		w.cleanupJobAcquisitionTokenSecret(ctx, log, kjob)
		w.checkFinished(ctx, log, jobUUID, kjob)
		return
	}

	// Register for cancellation checks while there is no pod. podWatcher only
	// sees jobs once a pod exists, so without this a Job suspended by an
	// admission controller is never checked: cancelling the build does nothing,
	// and the job still runs whenever it is eventually admitted.
	if !hasPod(kjob) {
		w.addToBuildkiteJobChecker(jobUUID, kjob)
	}

	w.checkStalledWithoutPod(log, jobUUID, kjob)
}

func (w *jobWatcher) cleanupJobAcquisitionTokenSecret(ctx context.Context, log *slog.Logger, kjob *batchv1.Job) {
	name := kjob.Annotations[config.JobAcquisitionTokenSecretAnnotation]
	if name == "" {
		return
	}
	if err := w.k8s.CoreV1().Secrets(kjob.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
		log.Warn("Failed to delete job acquisition token secret", "error", err)
	}
}

func (w *jobWatcher) addToBuildkiteJobChecker(jobUUID uuid.UUID, kjob *batchv1.Job) {
	if w.bkJobChecker == nil {
		return
	}
	w.bkJobChecker.AddK8sJob(jobUUID, kjob.ObjectMeta)
}

func (w *jobWatcher) stopCheckingBuildkiteJob(jobUUID uuid.UUID) {
	if w.bkJobChecker == nil {
		return
	}
	w.bkJobChecker.StopCheckingJob(jobUUID)
}

// checkFinished inspects a finished K8s Job and, when needed, fails the
// corresponding Buildkite job to release its reservation.
func (w *jobWatcher) checkFinished(ctx context.Context, log *slog.Logger, jobUUID uuid.UUID, kjob *batchv1.Job) {
	log.Debug("Checking finished job")

	// Successful: the agent ran and reported job state to BK itself.
	if kjob.Status.Succeeded > 0 {
		return
	}

	// Pods were terminated due to ActiveDeadlineSeconds being set on the
	// k8s Job. completionsWatcher (or cleanupStalledJob) sets that to clean
	// up after the agent has already reported job state to BK.
	for _, cond := range kjob.Status.Conditions {
		if cond.Reason == batchv1.JobReasonDeadlineExceeded && kjob.Spec.ActiveDeadlineSeconds != nil {
			return
		}
	}

	// A pod was created and failed. If BK still has the job in Reserved state,
	// the agent never acquired it - fail the BK job ourselves
	// to release the reservation immediately rather than waiting for the BK
	// reservation TTL (~15 min).
	if kjob.Status.Failed > 0 {
		w.failBeforeAgentAcquire(ctx, log, jobUUID, kjob)
		return
	}

	// No pod was ever created.
	jobWatcherFinishedWithoutPodCounter.Inc()
	log.Info("The Kubernetes job ended without starting a pod. Failing the corresponding Buildkite job")
	message := "The Kubernetes job ended without starting a pod.\n"
	message += w.fetchEvents(ctx, log, kjob)
	w.failJob(ctx, log, kjob, message)
}

// failBeforeAgentAcquire is called when a k8s Job has finished with a failed
// pod. If BK still has the job in Reserved state, the agent
// never acquired it - so we fail the BK job to release the reservation.
func (w *jobWatcher) failBeforeAgentAcquire(ctx context.Context, log *slog.Logger, jobUUID uuid.UUID, kjob *batchv1.Job) {
	state, _, err := w.agentClient.GetJobState(ctx, jobUUID.String())
	if err != nil {
		log.Warn("Failed to fetch BK job state; skipping pre-agent failure check", "error", err)
		return
	}

	if state.State != api.JobStateReserved {
		// Only act when the job is still in the Reserved state. This stack
		// reserved it; the agent never got far enough to acquire. Any other
		// state means either the agent took it (Running, Finished etc.) or
		// the reservation was already released (Scheduled), in which case
		// BK is the source of truth and we shouldn't interfere.
		return
	}

	jobWatcherFailedBeforeAgentAcquireCounter.Inc()
	log.Info("Kubernetes pod failed before the buildkite-agent acquired the Buildkite job. Failing the BK job to release the reservation.", "bk_job_state", string(state.State))
	message := "The Kubernetes pod failed before the buildkite-agent acquired this Buildkite job. The pod was likely killed by Kubernetes (eviction, OOM, node failure) or terminated externally before the agent could start.\n"
	message += w.fetchEvents(ctx, log, kjob)
	w.failJob(ctx, log, kjob, message)
	w.ignoreJob(jobUUID)
}

func (w *jobWatcher) checkStalledWithoutPod(log *slog.Logger, jobUUID uuid.UUID, kjob *batchv1.Job) {
	log.Debug("Checking job for stalling without a pod")

	// If the job is not finished and there is no pod, it should start one
	// before too long. Otherwise the job is stalled.
	if hasPod(kjob) {
		// All's well with the world.
		w.removeFromStalling(jobUUID)
		return
	}

	if kjob.Status.StartTime == nil {
		// the _job_ hasn't even started?
		return
	}

	w.addToStalling(jobUUID, kjob)
}

// hasPod reports whether the job controller has ever observed a pod for the
// k8s Job, in any lifecycle stage: pending/running (Active), terminating
// (deletionTimestamp set but not yet terminal), or terminated — both
// accounted (Succeeded, Failed) and not yet accounted
// (UncountedTerminatedPods). False means the Job never produced a pod.
// Ready is a subset of Active, so it is not summed.
func hasPod(kjob *batchv1.Job) bool {
	pods := kjob.Status.Active + kjob.Status.Failed + kjob.Status.Succeeded
	if t := kjob.Status.Terminating; t != nil {
		pods += *t
	}
	if utp := kjob.Status.UncountedTerminatedPods; utp != nil {
		pods += int32(len(utp.Succeeded))
		pods += int32(len(utp.Failed))
	}
	return pods > 0
}

func (w *jobWatcher) fetchEvents(ctx context.Context, log *slog.Logger, kjob *batchv1.Job) string {
	// List the events for the job, which might contain useful info for
	// diagnosing the problem.
	events := w.k8s.CoreV1().Events(w.cfg.Namespace)
	evlist, err := events.List(ctx, metav1.ListOptions{
		// These events are only used for diagnostic messages, so serve the
		// read from the API server's watch cache (ResourceVersion "0")
		// instead of forcing a quorum read from etcd.
		ResourceVersion: "0",
		FieldSelector: fields.AndSelectors(
			fields.OneTermEqualSelector("involvedObject.kind", "Job"),
			fields.OneTermEqualSelector("involvedObject.name", kjob.Name),
		).String(),
	})
	if err != nil {
		log.Error("Couldn't get events for job", "error", err)
		return fmt.Sprintf("Couldn't get events for job %s: %v", kjob.Name, err)
	}
	if evlist == nil {
		return ""
	}

	return w.formatEvents(evlist)
}

func (w *jobWatcher) failJob(ctx context.Context, log *slog.Logger, kjob *batchv1.Job, message string) {
	failureInfo := FailureInfo{
		Message: message,
		// We can know almost all failures triggered by job watcher are stack related error.
		Reason: agent.SignalReasonStackError,
	}
	if err := failForK8sObject(ctx, log, kjob, failureInfo, w.agentClient); err != nil {
		// Maybe the job was cancelled in the meantime?
		log.Error("Could not fail Buildkite job", "error", err)
		jobWatcherBuildkiteJobFailErrorsCounter.Inc()
		return
	}
	jobWatcherBuildkiteJobFailsCounter.Inc()
}

func (w *jobWatcher) formatEvents(evlist *corev1.EventList) string {
	if len(evlist.Items) == 0 {
		return "Events: none"
	}

	tw := table.NewWriter()
	tw.SetStyle(table.StyleRounded)
	tw.AppendHeader(table.Row{"LAST EVENT", "REPEATED", "TYPE", "REASON", "MESSAGE"})
	tw.SetColumnConfigs([]table.ColumnConfig{
		{Number: 5, WidthMax: 50, WidthMaxEnforcer: text.WrapSoft}, // Set the max width of the message column to 50 and soft wrap it
	})
	tw.AppendSeparator()
	for _, event := range evlist.Items {
		// Events can be produced by either the new-style recorder
		// (EventTime + Series) or the old-style recorder
		// (FirstTimestamp/LastTimestamp + Count). Use whichever is set.
		eventTime := event.EventTime.Time
		if eventTime.IsZero() {
			eventTime = event.FirstTimestamp.Time
		}

		if event.Series == nil {
			var repeated string
			if event.Count > 1 {
				repeated = fmt.Sprintf("x%d", event.Count)
				eventTime = event.LastTimestamp.Time
			} else {
				repeated = "-"
			}
			tw.AppendRow(table.Row{eventTime, repeated, event.Type, event.Reason, event.Message})
			continue
		}

		lastTime := event.Series.LastObservedTime.Time
		firstToLast := duration.HumanDuration(lastTime.Sub(eventTime))
		countMsg := fmt.Sprintf("x%d over %s", event.Series.Count, firstToLast)
		tw.AppendRow(table.Row{lastTime, countMsg, event.Type, event.Reason, event.Message})
	}
	return tw.Render()
}

func (w *jobWatcher) addToStalling(jobUUID uuid.UUID, kjob *batchv1.Job) {
	w.stallingJobsMu.Lock()
	defer w.stallingJobsMu.Unlock()
	w.stallingJobs[jobUUID] = kjob
}

func (w *jobWatcher) removeFromStalling(jobUUID uuid.UUID) {
	w.stallingJobsMu.Lock()
	defer w.stallingJobsMu.Unlock()
	delete(w.stallingJobs, jobUUID)
}

// removeCandidateFromStalling removes the stalling entry for jobUUID only if
// it still refers to the same k8s Job as the checked candidate. An informer
// event may have replaced the entry with a recreated Job (same deterministic
// name, different UID) while the candidate was being checked against the
// APIs; that replacement must stay and age through its own grace period.
func (w *jobWatcher) removeCandidateFromStalling(jobUUID uuid.UUID, candidate *batchv1.Job) {
	w.stallingJobsMu.Lock()
	defer w.stallingJobsMu.Unlock()
	if current, ok := w.stallingJobs[jobUUID]; ok && current.UID == candidate.UID {
		delete(w.stallingJobs, jobUUID)
	}
}

func (w *jobWatcher) stalledJobChecker(ctx context.Context) {
	ticker := time.Tick(time.Second)
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker:
			// continue below
		}

		w.cleanupStalledJobs(ctx)
	}
}

// cleanupStalledJobs runs one pass over the stalling jobs, cleaning up those
// that have exceeded the grace period without starting a pod.
func (w *jobWatcher) cleanupStalledJobs(ctx context.Context) {
	// Gather jobs past the grace period. API calls happen after unlocking.
	candidates := make(map[uuid.UUID]*batchv1.Job)
	w.stallingJobsMu.Lock()
	for jobUUID, kjob := range w.stallingJobs {
		if time.Since(kjob.Status.StartTime.Time) < w.cfg.EmptyJobGracePeriod {
			continue
		}
		candidates[jobUUID] = kjob
	}
	w.stallingJobsMu.Unlock()

	var stalled []*batchv1.Job
	for jobUUID, kjob := range candidates {
		log := loggerForObject(w.logger, kjob)

		reap, err := w.confirmStalled(ctx, log, jobUUID, kjob)
		switch {
		case err != nil:
			// Leave it in stallingJobs so the next cycle retries.
			continue

		case !reap:
			// A false positive from a stale informer cache. Once the cache
			// catches up, the job leaves stallingJobs for good.
			w.removeCandidateFromStalling(jobUUID, kjob)

		default:
			// The k8s Job is a zombie holding a slot: no pod after the grace
			// period, and no agent working on the BK job. Two scenarios lead
			// here:
			// 1. The pod can't be scheduled (e.g. unsatisfiable constraints)
			//    and the BK job hasn't started (reserved, scheduled).
			// 2. The pod can't be scheduled, and the BK job was handled by
			//    something else in the meantime and reached a terminal state
			//    (canceled, expired, ...) or is no longer known to Buildkite.
			// Reap it, and ignore it from now until it is deleted.
			w.ignoreJob(jobUUID)
			w.removeFromStalling(jobUUID)
			stalled = append(stalled, kjob)
		}
	}

	jobWatcherStalledWithoutPodCounter.Add(float64(len(stalled)))

	// Fail BK jobs and delete k8s jobs.
	for _, kjob := range stalled {
		w.cleanupStalledJob(ctx, kjob)
	}
}

// confirmStalled reports whether a stall candidate is really stalled and
// should be reaped. The informer cache may be stale, so before doing anything
// destructive, double-check against two live sources:
//   - Buildkite: an agent actively working on the job implies a pod exists.
//   - The k8s API server: a pod may exist whose agent hasn't connected yet
//     (e.g. still pulling a large image), or the Job may already be gone,
//     leaving nothing to reap.
//
// An error means neither source could confirm the stall.
func (w *jobWatcher) confirmStalled(ctx context.Context, log *slog.Logger, jobUUID uuid.UUID, kjob *batchv1.Job) (bool, error) {
	active, state, err := w.isJobActive(ctx, jobUUID)
	if err != nil {
		log.Warn("Failed to fetch BK job state; skipping stalled job cleanup to avoid killing a potentially running job", "error", err)
		return false, err
	}
	if active {
		log.Info("Skipping stalled job cleanup: an agent is working on the Buildkite job (informer cache was likely stale)", "bk_job_state", string(state))
		jobWatcherStalledCleanupSkippedCounter.WithLabelValues("agent_active").Inc()
		return false, nil
	}

	job, err := w.k8s.BatchV1().Jobs(kjob.Namespace).Get(ctx, kjob.Name, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		// There is no zombie k8s Job holding a slot, so there is nothing to
		// reap. OnDelete tidies the bookkeeping, the deduper drops the UUID,
		// and a still-scheduled BK job gets recreated from a later jobs
		// query. Failing the BK job here would foreclose that retry.
		log.Info("Skipping stalled job cleanup: the k8s Job no longer exists", "bk_job_state", string(state))
		jobWatcherStalledCleanupSkippedCounter.WithLabelValues("job_gone").Inc()
		return false, nil
	}
	if err != nil {
		log.Warn("Failed to fetch job from the API server; skipping stalled job cleanup to avoid killing a potentially starting pod", "error", err)
		return false, err
	}
	if job.UID != kjob.UID {
		// Job names are deterministic per Buildkite job UUID: the candidate
		// was deleted and a replacement Job was already created under the
		// same name. Reaping the replacement with the old candidate's
		// expired grace period would foreclose that retry.
		log.Info("Skipping stalled job cleanup: the k8s Job was replaced by a newer Job with the same name", "bk_job_state", string(state))
		jobWatcherStalledCleanupSkippedCounter.WithLabelValues("job_replaced").Inc()
		return false, nil
	}
	if hasPod(job) {
		log.Info("Skipping stalled job cleanup: the job has pods (informer cache was stale)", "bk_job_state", string(state))
		jobWatcherStalledCleanupSkippedCounter.WithLabelValues("pod_exists").Inc()
		return false, nil
	}

	return true, nil
}

// isJobActive reports whether an agent is actively working on the Buildkite
// job, which implies a pod exists even if the informer cache says otherwise.
// The job state is also returned for logging.
func (w *jobWatcher) isJobActive(ctx context.Context, jobUUID uuid.UUID) (bool, api.JobState, error) {
	state, _, err := w.agentClient.GetJobState(ctx, jobUUID.String())
	if err != nil {
		return false, "", err
	}
	switch state.State {
	case api.JobStateAccepted, api.JobStateAssigned, api.JobStateRunning,
		api.JobStateCanceling, api.JobStateTimingOut:
		return true, state.State, nil
	}
	return false, state.State, nil
}

func (w *jobWatcher) cleanupStalledJob(ctx context.Context, kjob *batchv1.Job) {
	log := loggerForObject(w.logger, kjob)

	// Fetch events for the failure message, and try to fail the job.
	// failJob may fail (e.g. the BK job already finished); proceed anyway —
	// the zombie k8s Job still needs to be reaped.
	stallDuration := duration.HumanDuration(time.Since(kjob.Status.StartTime.Time))
	message := fmt.Sprintf("The Kubernetes job spent %s without starting a pod.\n", stallDuration)
	message += w.fetchEvents(ctx, log, kjob)
	w.failJob(ctx, log, kjob, message)

	// Use ActiveDeadlineSeconds to fail the job, which makes k8s delete the job
	// in the future.
	// If we delete it straight away, the deduper will remove it from its map,
	// letting the job (still in BK state "scheduled") be recreated immediately
	// even though the job is failed on BK first, because it might still be in
	// the most recent jobs query, waiting for the staleness timeout.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		job, err := w.k8s.BatchV1().Jobs(kjob.Namespace).Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		// activeDeadlineSeconds applies from the start of the job. But the
		// job is only cleaned up though TTLSecondsAfterFinished, which is way
		// in the future.
		job.Spec.ActiveDeadlineSeconds = new(int64(1))
		_, err = w.k8s.BatchV1().Jobs(kjob.Namespace).Update(ctx, job, metav1.UpdateOptions{})
		return err
	}); err != nil {
		jobWatcherJobCleanupErrorsCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()
		w.logger.Error("failed to update job with ActiveDeadlineSeconds", "error", err)
		return
	}
	jobWatcherJobCleanupsCounter.Inc()
}

func (w *jobWatcher) ignoreJob(jobUUID uuid.UUID) {
	w.ignoredJobsMu.Lock()
	defer w.ignoredJobsMu.Unlock()
	w.ignoredJobs[jobUUID] = struct{}{}
}

func (w *jobWatcher) unignoreJob(jobUUID uuid.UUID) {
	w.ignoredJobsMu.Lock()
	defer w.ignoredJobsMu.Unlock()
	delete(w.ignoredJobs, jobUUID)
}

func (w *jobWatcher) isIgnored(jobUUID uuid.UUID) bool {
	w.ignoredJobsMu.RLock()
	defer w.ignoredJobsMu.RUnlock()
	_, ignore := w.ignoredJobs[jobUUID]
	return ignore
}
