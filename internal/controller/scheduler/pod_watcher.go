package scheduler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	agentcore "github.com/buildkite/agent/v3/core"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
)

type podWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface
	gql    graphql.Client
	cfg    *config.Config

	// ImagePullBackOff detection waits at least this duration after pod
	// creation before it cancels the job.
	imagePullBackOffGracePeriod time.Duration

	// Jobs that we've failed, cancelled, or were found to be in a terminal
	// state.
	ignoredJobsMu sync.RWMutex
	ignoredJob    map[uuid.UUID]struct{}

	// The job cancel checkers query the job state every so often.
	jobCancelCheckerInterval time.Duration

	// Channels that are closed when a cancel checker should stop.
	cancelCheckerChsMu sync.Mutex
	cancelCheckerChs   map[uuid.UUID]*onceChan

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure job cancel checkers are cleaned up.
	resourceEventHandlerCtx context.Context

	agentTags map[string]string
}

// NewPodWatcher creates an informer that does various things with pods and
// Buildkite jobs:
//   - If an init container fails, the BK Agent REST API will be used to fail
//     the job (since an agent hasn't run yet).
//   - If a container stays in ImagePullBackOff state for too long, the BK
//     Agent REST API will be used to fail the job and the pod will be evicted.
//   - If a container stays in ImagePullBackOff, and the pod somehow got through
//     all the init containers (including the image pull checks...) the BK
//     GraphQL API will be used to cancel the job instead.
//   - If a pod is pending, every so often Buildkite will be checked to see if
//     the corresponding job has been cancelled so that the pod can be evicted
//     early.
func NewPodWatcher(logger *zap.Logger, k8s kubernetes.Interface, cfg *config.Config) *podWatcher {
	imagePullBackOffGracePeriod := cfg.ImagePullBackOffGracePeriod
	if imagePullBackOffGracePeriod <= 0 {
		imagePullBackOffGracePeriod = config.DefaultImagePullBackOffGracePeriod
	}
	jobCancelCheckerInterval := cfg.JobCancelCheckerPollInterval
	if jobCancelCheckerInterval <= 0 {
		jobCancelCheckerInterval = config.DefaultJobCancelCheckerPollInterval
	}

	agentTags, errs := agenttags.TagMapFromTags(cfg.Tags)
	if len(errs) > 0 {
		logger.Warn("parsing agent tags", zap.Errors("errors", errs))
	}

	pw := &podWatcher{
		logger:                      logger,
		k8s:                         k8s,
		gql:                         api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
		cfg:                         cfg,
		imagePullBackOffGracePeriod: imagePullBackOffGracePeriod,
		jobCancelCheckerInterval:    jobCancelCheckerInterval,
		ignoredJob:                  make(map[uuid.UUID]struct{}),
		cancelCheckerChs:            make(map[uuid.UUID]*onceChan),
		agentTags:                   agentTags,
	}
	ignoredJobsGaugeFunc = func() int {
		pw.ignoredJobsMu.RLock()
		defer pw.ignoredJobsMu.RUnlock()
		return len(pw.ignoredJob)
	}
	jobCancelCheckerGaugeFunc = func() int {
		pw.cancelCheckerChsMu.Lock()
		defer pw.cancelCheckerChsMu.Unlock()
		return len(pw.cancelCheckerChs)
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
	return nil
}

func (w *podWatcher) OnDelete(maybePod any) {
	podWatcherOnDeleteEventCounter.Inc()

	pod, wasPod := maybePod.(*corev1.Pod)
	if !wasPod {
		return
	}

	log := loggerForObject(w.logger, pod)
	jobUUID, err := jobUUIDForObject(pod)
	if err != nil {
		log.Error("Job UUID label missing or invalid for pod")
		return
	}

	w.stopJobCancelChecker(jobUUID)

	// The pod is gone, so we can stop ignoring it (if it comes back).
	w.unignoreJob(jobUUID)
}

func (w *podWatcher) OnAdd(maybePod any, isInInitialList bool) {
	podWatcherOnAddEventCounter.Inc()

	pod, wasPod := maybePod.(*corev1.Pod)
	if !wasPod {
		return
	}

	w.runChecks(w.resourceEventHandlerCtx, pod)
}

func (w *podWatcher) OnUpdate(oldMaybePod, newMaybePod any) {
	podWatcherOnUpdateEventCounter.Inc()

	oldPod, oldWasPod := newMaybePod.(*corev1.Pod)
	newPod, newWasPod := newMaybePod.(*corev1.Pod)

	// This nonsense statement is only necessary because the types are too loose.
	// Most likely both old and new are going to be Pods.
	switch {
	case newWasPod:
		w.runChecks(w.resourceEventHandlerCtx, newPod)

	case oldWasPod:
		w.runChecks(w.resourceEventHandlerCtx, oldPod)
	}
}

func (w *podWatcher) runChecks(ctx context.Context, pod *corev1.Pod) {
	log := loggerForObject(w.logger, pod)
	jobUUID, err := jobUUIDForObject(pod)
	if err != nil {
		log.Error("Job UUID label missing or invalid for pod")
		return
	}

	if w.isIgnored(jobUUID) {
		log.Debug("Job is currently ignored for podWatcher checks")
		return
	}

	// Check for an init container that failed for any reason.
	// (Note: users can define their own init containers through podSpec.)
	w.failOnInitContainerFailure(ctx, log, pod)

	// Check for a container stuck in ImagePullBackOff or InvalidImageName,
	// and fail or cancel the job accordingly.
	w.failOnImagePullFailure(ctx, log, pod, jobUUID)

	// Check whether the agent container has started yet, and start or stop the
	// job cancel checker accordingly.
	w.startOrStopJobCancelChecker(ctx, log, pod, jobUUID)
}

func (w *podWatcher) failOnImagePullFailure(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID) {
	log.Debug("Checking pod containers for ImagePullBackOff or InvalidImageName")

	failImmediately := false

	images := make(map[string]struct{})

	// If any init container fails to pull, whether it's one we added
	// specifically to check for pull failure, the pod won't run.
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		waiting := containerStatus.State.Waiting
		if waiting == nil {
			continue
		}
		switch waiting.Reason {
		case "ImagePullBackOff":
			images[containerStatus.Image] = struct{}{}
		case "InvalidImageName":
			images[containerStatus.Image] = struct{}{}
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
		case "ImagePullBackOff":
			if !isSystemContainer(&containerStatus) {
				log.Info("Ignoring container during ImagePullBackOff watch.", zap.String("name", containerStatus.Name))
				continue
			}
			images[containerStatus.Image] = struct{}{}
		case "InvalidImageName":
			images[containerStatus.Image] = struct{}{}
			failImmediately = true
		}
	}

	if len(images) == 0 {
		// All's well with the world.
		return
	}

	if !failImmediately { // apply the grace period
		if pod.Status.StartTime == nil {
			// Status could be unpopulated, or it hasn't started yet.
			return
		}
		startedAt := pod.Status.StartTime.Time
		if startedAt.IsZero() || time.Since(startedAt) < w.imagePullBackOffGracePeriod {
			// Not started yet, or started recently
			return
		}
	}

	// Get the current job state from BK.
	// What we do next depends on what state it is in.
	resp, err := api.GetCommandJob(ctx, w.gql, jobUUID.String())
	if err != nil {
		log.Warn("Failed to query command job", zap.Error(err))
		return
	}
	job, ok := resp.Job.(*api.GetCommandJobJobJobTypeCommand)
	if !ok {
		log.Warn("Job was not a command job")
		return
	}

	log = log.With(zap.String("job_state", string(job.State)))

	switch job.State {
	case api.JobStatesScheduled:
		// We can acquire it and fail it ourselves.
		log.Info("One or more job containers are in ImagePullBackOff. Failing.")
		message := w.formatImagePullFailureMessage(images)
		switch err := acquireAndFailForObject(ctx, log, w.k8s, w.cfg, pod, message); {
		case errors.Is(err, agentcore.ErrJobAcquisitionRejected):
			podWatcherBuildkiteJobFailErrorsCounter.Inc()
			// If the error was because BK rejected the job acquisition, then
			// it's moved on to a state where we need to cancel instead.
			// (The init container probably successfully pulled, but another
			// pull of the same image later on failed after the agent started.)
			log.Info("Attempting to cancel job instead")
			w.cancelJob(ctx, log, pod, jobUUID)
			return

		case err != nil:
			podWatcherBuildkiteJobFailErrorsCounter.Inc()

			// Maybe the job was cancelled in the meantime?
			log.Error("Could not fail Buildkite job", zap.Error(err))
			return
		}
		podWatcherBuildkiteJobFailsCounter.Inc()
		// Also evict the pod, because it won't die on its own.
		w.evictPod(ctx, log, pod, jobUUID)

	case api.JobStatesAccepted, api.JobStatesAssigned, api.JobStatesRunning:
		// An agent is already doing something with the job - now canceling
		// is the only lever available.
		log.Info("One or more job containers are in ImagePullBackOff. Cancelling.")
		w.cancelJob(ctx, log, pod, jobUUID)

	case api.JobStatesCanceling, api.JobStatesCanceled, api.JobStatesFinished, api.JobStatesSkipped:
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

func (w *podWatcher) failOnInitContainerFailure(ctx context.Context, log *zap.Logger, pod *corev1.Pod) {
	log.Debug("Checking pod for failed init containers")

	containerFails := make(map[string]*corev1.ContainerStateTerminated)

	// If any init container fails, whether it's one we added specifically to
	// check for pull failure or not, the pod won't run.
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		term := containerStatus.State.Terminated
		if term == nil || term.ExitCode == 0 { // not terminated, or succeeded
			continue
		}
		containerFails[containerStatus.Name] = term
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
	if err := acquireAndFailForObject(ctx, log, w.k8s, w.cfg, pod, message); err != nil {
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

func (w *podWatcher) formatImagePullFailureMessage(images map[string]struct{}) string {
	// Format the failed images into a nice sorted list.
	imagesList := make([]string, 0, len(images))
	for image := range images {
		imagesList = append(imagesList, image)
	}
	slices.Sort(imagesList)
	var message strings.Builder
	message.WriteString("The following container images couldn't be pulled:\n")
	for _, image := range imagesList {
		fmt.Fprintf(&message, " * %q\n", image)
	}
	return message.String()
}

func (w *podWatcher) evictPod(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID) {
	eviction := &policyv1.Eviction{
		ObjectMeta: pod.ObjectMeta,
	}
	if err := w.k8s.PolicyV1().Evictions(w.cfg.Namespace).Evict(ctx, eviction); err != nil {
		podEvictionErrorsCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()
		log.Error("Couldn't evict pod", zap.Error(err))
		return
	}
	podsEvictedCounter.WithLabelValues("image_pull_failure").Inc()

	// Because eviction isn't instantaneous, the pod can continue to exist
	// for a bit. Record that we've failed the job to avoid trying to fail
	// it again.
	w.ignoreJob(jobUUID)
}

func (w *podWatcher) cancelJob(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID) {
	_, err := api.CancelCommandJob(ctx, w.gql, api.JobTypeCommandCancelInput{
		ClientMutationId: pod.Name,
		Id:               jobUUID.String(),
	})
	if err != nil {
		log.Warn("Failed to cancel command job", zap.Error(err))
		podWatcherBuildkiteJobCancelErrorsCounter.Inc()
		// Could be network problems
		// Could be in non-cancelable state
		// Try again later?
		return
	}
	podWatcherBuildkiteJobCancelsCounter.Inc()

	// Note that evicting the pod might prevent the agent from logging its
	// last-gasp "it could be ImagePullBackOff" message.

	// We can avoid repeating the GraphQL queries to fetch and cancel the job
	// (between cancelling and Kubernetes cleaning up the pod) if we got here.
	w.ignoreJob(jobUUID)
}

func (w *podWatcher) startOrStopJobCancelChecker(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID) {
	switch pod.Status.Phase {
	case corev1.PodPending:
		w.startJobCancelChecker(ctx, log, pod.ObjectMeta, jobUUID)

	default:
		// Running: the agent container has started or is about to start, and it
		//          can handle the cancellation and exit.
		// Succeeded, Failed: it's already over.
		// Unknown: probably shouldn't interfere.
		w.stopJobCancelChecker(jobUUID)
	}
}

func (w *podWatcher) startJobCancelChecker(ctx context.Context, log *zap.Logger, podMeta metav1.ObjectMeta, jobUUID uuid.UUID) {
	w.cancelCheckerChsMu.Lock()
	defer w.cancelCheckerChsMu.Unlock()

	if w.cancelCheckerChs[jobUUID] != nil {
		// The checker is already running or has run.
		return
	}
	stopCh := make(chan struct{})
	w.cancelCheckerChs[jobUUID] = &onceChan{ch: stopCh}
	go w.jobCancelChecker(ctx, stopCh, log, podMeta, jobUUID)
}

func (w *podWatcher) stopJobCancelChecker(jobUUID uuid.UUID) {
	w.cancelCheckerChsMu.Lock()
	defer w.cancelCheckerChsMu.Unlock()
	w.cancelCheckerChs[jobUUID].closeOnce()
	delete(w.cancelCheckerChs, jobUUID)
}

// jobCancelChecker runs a loop that queries Buildkite for the job state, and
// evicts the pod if the job becomes cancelled. This should only be used for
// pods that are still pending: stopCh should be closed as soon as the agent
// container starts running.
func (w *podWatcher) jobCancelChecker(ctx context.Context, stopCh <-chan struct{}, log *zap.Logger, podMeta metav1.ObjectMeta, jobUUID uuid.UUID) {
	log.Debug("Checking job state for cancellation")
	defer log.Debug("Stopped checking job state for cancellation")

	ticker := time.NewTicker(w.jobCancelCheckerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-stopCh:
			return

		case <-ticker.C:
			resp, err := api.GetCommandJob(ctx, w.gql, jobUUID.String())
			if err != nil {
				// *shrug* Check again soon.
				continue
			}
			job, ok := resp.Job.(*api.GetCommandJobJobJobTypeCommand)
			if !ok {
				log.Warn("Job was not a command job")
				continue
			}
			log = log.With(zap.String("job_state", string(job.State)))

			switch job.State {
			case api.JobStatesCanceled, api.JobStatesCanceling:
				log.Info("Evicting pending pod for cancelled job")
				eviction := &policyv1.Eviction{ObjectMeta: podMeta}
				if err := w.k8s.PolicyV1().Evictions(w.cfg.Namespace).Evict(ctx, eviction); err != nil {
					log.Error("Couldn't evict pod", zap.Error(err))
					podEvictionErrorsCounter.WithLabelValues("bk_job_cancelled", string(kerrors.ReasonForError(err))).Inc()
					continue
				}
				podsEvictedCounter.WithLabelValues("bk_job_cancelled").Inc()
				return

			case api.JobStatesScheduled:
				// The pod can continue waiting for resources / initializing.

			default:
				// Assigned, Accepted, Running: Too late. Let the agent within
				// the pod handle cancellation. Finished, etc: it's already over.
				// If it's any other state, we probably shouldn't interfere.
				log.Debug("Ending job cancel checker due to job state")
				return
			}
		}
	}
}

func (w *podWatcher) ignoreJob(jobUUID uuid.UUID) {
	w.ignoredJobsMu.Lock()
	defer w.ignoredJobsMu.Unlock()
	w.ignoredJob[jobUUID] = struct{}{}
}

func (w *podWatcher) unignoreJob(jobUUID uuid.UUID) {
	w.ignoredJobsMu.Lock()
	defer w.ignoredJobsMu.Unlock()
	delete(w.ignoredJob, jobUUID)
}

func (w *podWatcher) isIgnored(jobUUID uuid.UUID) bool {
	w.ignoredJobsMu.RLock()
	defer w.ignoredJobsMu.RUnlock()
	_, ignore := w.ignoredJob[jobUUID]
	return ignore
}

// onceChan stores a channel and a [sync.Once] to be used for closing the
// channel at most once.
type onceChan struct {
	once sync.Once
	ch   chan struct{}
}

func (oc *onceChan) closeOnce() {
	if oc == nil {
		return
	}
	oc.once.Do(func() { close(oc.ch) })
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
