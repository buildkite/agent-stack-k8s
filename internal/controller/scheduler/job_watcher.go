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
	"k8s.io/utils/ptr"
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
func NewJobWatcher(logger *slog.Logger, k8sClient kubernetes.Interface, agentClient *api.AgentClient, cfg *config.Config) *jobWatcher {
	w := &jobWatcher{
		logger:       logger,
		k8s:          k8sClient,
		agentClient:  agentClient,
		cfg:          cfg,
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

	jobUUID, err := jobUUIDForObject(kjob)
	if err != nil {
		return
	}

	w.removeFromStalling(jobUUID)

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
		w.checkFinished(ctx, log, jobUUID, kjob)
	} else {
		w.checkStalledWithoutPod(log, jobUUID, kjob)
	}
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
	pods := kjob.Status.Active + kjob.Status.Failed + kjob.Status.Succeeded
	// Ready and Terminating are subsets of Active (I think)
	if utp := kjob.Status.UncountedTerminatedPods; utp != nil {
		pods += int32(len(utp.Succeeded))
		pods += int32(len(utp.Failed))
	}
	if pods > 0 {
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

func (w *jobWatcher) fetchEvents(ctx context.Context, log *slog.Logger, kjob *batchv1.Job) string {
	// List the events for the job, which might contain useful info for
	// diagnosing the problem.
	events := w.k8s.CoreV1().Events(w.cfg.Namespace)
	evlist, err := events.List(ctx, metav1.ListOptions{
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

func (w *jobWatcher) stalledJobChecker(ctx context.Context) {
	ticker := time.Tick(time.Second)
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker:
			// continue below
		}

		// Gather stalled jobs
		var stalled []*batchv1.Job
		w.stallingJobsMu.Lock()
		for jobUUID, kjob := range w.stallingJobs {
			if time.Since(kjob.Status.StartTime.Time) < w.cfg.EmptyJobGracePeriod {
				continue
			}

			// ignore it from now until it is deleted
			w.ignoreJob(jobUUID)

			// Move it from w.stalling into stalled
			stalled = append(stalled, kjob)
			delete(w.stallingJobs, jobUUID)
		}
		w.stallingJobsMu.Unlock()

		jobWatcherStalledWithoutPodCounter.Add(float64(len(stalled)))

		// Fail BK jobs and delete k8s jobs.
		for _, kjob := range stalled {
			w.cleanupStalledJob(ctx, kjob)
		}
	}
}

func (w *jobWatcher) cleanupStalledJob(ctx context.Context, kjob *batchv1.Job) {
	log := loggerForObject(w.logger, kjob)

	// Fetch events for the failure message, and try to fail the job.
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
		job.Spec.ActiveDeadlineSeconds = ptr.To[int64](1)
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
