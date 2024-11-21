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
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
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
	ignoreJobsMu sync.RWMutex
	ignoreJobs   map[uuid.UUID]struct{}

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
//   - If a container stays in ImagePullBackOff state for too long, the Buildkite
//     Agent REST API will be used to fail the job and the pod will be evicted.
//   - If a container stays in ImagePullBackOff, and the pod somehow got through
//     all the init containers (including the image pull checks...) the Buildkite
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

	return &podWatcher{
		logger:                      logger,
		k8s:                         k8s,
		gql:                         api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
		cfg:                         cfg,
		imagePullBackOffGracePeriod: imagePullBackOffGracePeriod,
		jobCancelCheckerInterval:    jobCancelCheckerInterval,
		ignoreJobs:                  make(map[uuid.UUID]struct{}),
		cancelCheckerChs:            make(map[uuid.UUID]*onceChan),
		agentTags:                   agentTags,
	}
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
	pod, wasPod := maybePod.(*corev1.Pod)
	if !wasPod {
		return
	}

	jobUUID, _, err := w.jobUUIDAndLogger(pod)
	if err != nil {
		return
	}

	w.stopJobCancelChecker(jobUUID)
}

func (w *podWatcher) OnAdd(maybePod any, isInInitialList bool) {
	pod, wasPod := maybePod.(*corev1.Pod)
	if !wasPod {
		return
	}

	w.runChecks(w.resourceEventHandlerCtx, pod)
}

func (w *podWatcher) OnUpdate(oldMaybePod, newMaybePod any) {
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
	jobUUID, log, err := w.jobUUIDAndLogger(pod)
	if err != nil {
		return
	}

	// Check for a container stuck in ImagePullBackOff, and fail or cancel
	// the job accordingly.
	w.cancelImagePullBackOff(ctx, log, pod, jobUUID)

	// Check whether the agent container has started yet, and start or stop the
	// job cancel checker accordingly.
	w.startOrStopJobCancelChecker(ctx, log, pod, jobUUID)
}

func (w *podWatcher) jobUUIDAndLogger(pod *corev1.Pod) (uuid.UUID, *zap.Logger, error) {
	log := w.logger.With(zap.String("namespace", pod.Namespace), zap.String("podName", pod.Name))

	rawJobUUID, exists := pod.Labels[config.UUIDLabel]
	if !exists {
		log.Debug("Job UUID label not present. Skipping.")
		return uuid.UUID{}, log, errors.New("no job UUID label")
	}

	jobUUID, err := uuid.Parse(rawJobUUID)
	if err != nil {
		log.Warn("Job UUID label was not a UUID!", zap.String("jobUUID", rawJobUUID))
		return uuid.UUID{}, log, err
	}

	log = log.With(zap.String("jobUUID", jobUUID.String()))

	// Check that tags match - there may be pods around that were created by
	// another controller using different tags.
	if !agenttags.JobTagsMatchAgentTags(agenttags.ScanLabels(pod.Labels), w.agentTags) {
		log.Debug("Pod labels do not match agent tags for this controller. Skipping.")
		return uuid.UUID{}, log, errors.New("pod labels do not match agent tags for this controller")
	}

	w.ignoreJobsMu.RLock()
	defer w.ignoreJobsMu.RUnlock()

	if _, ignore := w.ignoreJobs[jobUUID]; ignore {
		log.Debug("Job already failed, canceled, or wasn't in a failable/cancellable state")
		return jobUUID, log, errors.New("job ignored")
	}
	return jobUUID, log, nil
}

func (w *podWatcher) cancelImagePullBackOff(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID) {
	log.Debug("Checking pod for ImagePullBackOff")

	if pod.Status.StartTime == nil {
		// Status could be unpopulated, or it hasn't started yet.
		return
	}
	startedAt := pod.Status.StartTime.Time
	if startedAt.IsZero() || time.Since(startedAt) < w.imagePullBackOffGracePeriod {
		// Not started yet, or started recently
		return
	}

	images := make(map[string]struct{})

	// If any init container fails to pull, whether it's one we added
	// specifically to check for pull failure, the pod won't run.
	for _, containerStatus := range pod.Status.InitContainerStatuses {
		if !shouldCancel(&containerStatus) {
			continue
		}
		images[containerStatus.Image] = struct{}{}
	}

	// These containers only run after the init containers have run.
	// Theoretically this could still happen even if all the init containers
	// successfully pulled.
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if !shouldCancel(&containerStatus) {
			continue
		}
		if !isSystemContainer(&containerStatus) {
			log.Info("Ignoring container during ImagePullBackOff watch.", zap.String("name", containerStatus.Name))
			continue
		}
		images[containerStatus.Image] = struct{}{}
	}

	if len(images) == 0 {
		// All's well with the world.
		return
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
		w.failJob(ctx, log, pod, jobUUID, images)

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

func (w *podWatcher) failJob(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID, images map[string]struct{}) {
	agentToken, err := fetchAgentToken(ctx, w.logger, w.k8s, w.cfg.Namespace, w.cfg.AgentTokenSecret)
	if err != nil {
		log.Error("Couldn't fetch agent token in order to fail the job", zap.Error(err))
		return
	}

	// Format the failed images into a nice list.
	imagesList := make([]string, 0, len(images))
	for image := range images {
		imagesList = append(imagesList, image)
	}
	slices.Sort(imagesList)
	var message strings.Builder
	message.WriteString("The following container images couldn't be pulled:\n")
	for _, image := range imagesList {
		fmt.Fprintf(&message, " * %s\n", image)
	}

	// Tags are required order to connect the agent.
	tags := agenttags.TagsFromLabels(pod.Labels)
	opts := w.cfg.AgentConfig.ControllerOptions()

	if err := failJob(ctx, w.logger, agentToken, jobUUID.String(), tags, message.String(), opts...); err != nil {
		log.Error("Couldn't fail the job", zap.Error(err))
		// If the error was because BK rejected the acquisition, then its moved
		// on to a state where we need to cancel instead.
		if errors.Is(err, agentcore.ErrJobAcquisitionRejected) {
			log.Info("Attempting to cancel job instead")
			w.cancelJob(ctx, log, pod, jobUUID)
		}
		return
	}

	// Let's also evict the pod (request graceful termination).
	eviction := &policyv1.Eviction{
		ObjectMeta: pod.ObjectMeta,
	}
	if err := w.k8s.PolicyV1().Evictions(w.cfg.Namespace).Evict(ctx, eviction); err != nil {
		log.Error("Couldn't evict pod", zap.Error(err))
	}

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
		// Could be network problems
		// Could be in non-cancelable state
		// Try again later?
		return
	}

	// Evicting the pod might prevent the agent from logging its last-gasp
	// "it could be ImagePullBackOff" message.
	// On the other hand, not evicting the pod will probably leave it running
	// indefinitely if there are any sidecars.
	// TODO: experiment with adding eviction here.

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
				}
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
	w.ignoreJobsMu.Lock()
	defer w.ignoreJobsMu.Unlock()
	w.ignoreJobs[jobUUID] = struct{}{}
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

func shouldCancel(containerStatus *corev1.ContainerStatus) bool {
	return containerStatus.State.Waiting != nil &&
		containerStatus.State.Waiting.Reason == "ImagePullBackOff"
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
