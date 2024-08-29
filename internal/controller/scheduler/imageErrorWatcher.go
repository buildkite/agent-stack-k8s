package scheduler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	agentcore "github.com/buildkite/agent/v3/core"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
)

type imageErrorWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface
	gql    graphql.Client
	cfg    *config.Config

	// The imageErrorWatcher waits at least this duration after pod
	// creation before it cancels the job.
	gracePeriod time.Duration

	// Jobs that we've failed, cancelled, or were found to be in a terminal
	// state.
	ignoreJobs map[uuid.UUID]struct{}
}

// NewImageErrorWatcher creates an informer that will use the Buildkite
// GraphQL API to cancel jobs that have pods with containers in the
// ImagePullBackOff state
func NewImageErrorWatcher(
	logger *zap.Logger,
	k8s kubernetes.Interface,
	cfg *config.Config,
) *imageErrorWatcher {
	return &imageErrorWatcher{
		logger:      logger,
		k8s:         k8s,
		gql:         api.NewClient(cfg.BuildkiteToken),
		cfg:         cfg,
		gracePeriod: cfg.ImagePullBackOffGradePeriod,
		ignoreJobs:  make(map[uuid.UUID]struct{}),
	}
}

// Creates a Pods informer and registers the handler on it
func (w *imageErrorWatcher) RegisterInformer(
	ctx context.Context,
	factory informers.SharedInformerFactory,
) error {
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return err
	}
	go factory.Start(ctx.Done())
	return nil
}

func (w *imageErrorWatcher) OnDelete(obj any) {}

func (w *imageErrorWatcher) OnAdd(maybePod any, isInInitialList bool) {
	pod, wasPod := maybePod.(*corev1.Pod)
	if !wasPod {
		return
	}

	w.cancelImageError(context.Background(), pod)
}

func (w *imageErrorWatcher) OnUpdate(oldMaybePod, newMaybePod any) {
	oldPod, oldWasPod := newMaybePod.(*corev1.Pod)
	newPod, newWasPod := newMaybePod.(*corev1.Pod)

	// This nonsense statement is only necessary because the types are too loose.
	// Most likely both old and new are going to be Pods.
	if newWasPod {
		w.cancelImageError(context.Background(), newPod)
	} else if oldWasPod {
		w.cancelImageError(context.Background(), oldPod)
	}
}

func (w *imageErrorWatcher) cancelImageError(ctx context.Context, pod *corev1.Pod) {
	log := w.logger.With(zap.String("namespace", pod.Namespace), zap.String("podName", pod.Name))
	log.Debug("Checking pod for ImageErrors")

	if pod.Status.StartTime == nil {
		// Status could be unpopulated, or it hasn't started yet.
		return
	}

	startedAt := pod.Status.StartTime.Time
	if startedAt.IsZero() || time.Since(startedAt) < w.gracePeriod {
		// Not started yet, or started recently
		return
	}

	rawJobUUID, exists := pod.Labels[config.UUIDLabel]
	if !exists {
		log.Info("Job UUID label not present. Skipping.")
		return
	}

	jobUUID, err := uuid.Parse(rawJobUUID)
	if err != nil {
		log.Warn("Job UUID label was not a UUID!", zap.String("jobUUID", rawJobUUID))
		return
	}

	log = log.With(zap.String("jobUUID", jobUUID.String()))

	if _, done := w.ignoreJobs[jobUUID]; done {
		log.Debug("Job already failed, canceled, or wasn't in a failable/cancellable state")
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
			log.Info("Ignoring container during ImageError watch.", zap.String("name", containerStatus.Name))
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
		w.ignoreJobs[jobUUID] = struct{}{}

	default:
		// Most states don't make sense for a command job that we've started
		// a pod for (e.g. blocked, broken, expired, pending, waiting, ...)
		// Maybe the meanings of states has changed since this build?
		// Log a message but don't do anything.
		log.Warn("Job not in actionable state")
	}
}

func (w *imageErrorWatcher) failJob(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID, images map[string]struct{}) {
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
	var tags []string
	for key, value := range pod.Labels {
		k, has := strings.CutPrefix(key, "tag.buildkite.com/")
		if !has {
			continue
		}
		tags = append(tags, fmt.Sprintf("%s=%s", k, value))
	}

	if err := failJob(ctx, w.logger, agentToken, jobUUID.String(), tags, message.String()); err != nil {
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
	w.ignoreJobs[jobUUID] = struct{}{}
}

func (w *imageErrorWatcher) cancelJob(ctx context.Context, log *zap.Logger, pod *corev1.Pod, jobUUID uuid.UUID) {
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
	w.ignoreJobs[jobUUID] = struct{}{}
}

func shouldCancel(containerStatus *corev1.ContainerStatus) bool {
	if containerStatus.State.Waiting == nil {
		return false
	}

	switch containerStatus.State.Waiting.Reason {
	case "ImagePullBackOff", "InvalidImageName":
		return true
	}

	return false
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
