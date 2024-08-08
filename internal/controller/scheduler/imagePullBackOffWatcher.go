package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/uuid"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
)

type imagePullBackOffWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface
	gql    graphql.Client
	cfg    *config.Config

	// The imagePullBackOffWatcher waits at least this duration after pod
	// creation before it cancels the job.
	gracePeriod time.Duration
}

// NewImagePullBackOffWatcher creates an informer that will use the Buildkite
// GraphQL API to cancel jobs that have pods with containers in the
// ImagePullBackOff state
func NewImagePullBackOffWatcher(
	logger *zap.Logger,
	k8s kubernetes.Interface,
	cfg *config.Config,
) *imagePullBackOffWatcher {
	return &imagePullBackOffWatcher{
		logger:      logger,
		k8s:         k8s,
		gql:         api.NewClient(cfg.BuildkiteToken),
		cfg:         cfg,
		gracePeriod: cfg.ImagePullBackOffGradePeriod,
	}
}

// Creates a Pods informer and registers the handler on it
func (w *imagePullBackOffWatcher) RegisterInformer(
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

func (w *imagePullBackOffWatcher) OnDelete(obj any) {}

func (w *imagePullBackOffWatcher) OnAdd(maybePod any, isInInitialList bool) {
	pod, wasPod := maybePod.(*corev1.Pod)
	if !wasPod {
		return
	}

	w.cancelImagePullBackOff(context.Background(), pod)
}

func (w *imagePullBackOffWatcher) OnUpdate(oldMaybePod, newMaybePod any) {
	oldPod, oldWasPod := newMaybePod.(*corev1.Pod)
	newPod, newWasPod := newMaybePod.(*corev1.Pod)

	// This nonsense statement is only necessary because the types are too loose.
	// Most likely both old and new are going to be Pods.
	if newWasPod {
		w.cancelImagePullBackOff(context.Background(), newPod)
	} else if oldWasPod {
		w.cancelImagePullBackOff(context.Background(), oldPod)
	}
}

func (w *imagePullBackOffWatcher) cancelImagePullBackOff(ctx context.Context, pod *corev1.Pod) {
	log := w.logger.With(zap.String("namespace", pod.Namespace), zap.String("podName", pod.Name))
	log.Debug("Checking pod for ImagePullBackOff")

	if pod.Status.StartTime == nil {
		// Status could be unpopulated, or it hasn't started yet.
		return
	}
	startedAt := pod.Status.StartTime.Time
	if startedAt.IsZero() || time.Since(startedAt) < w.gracePeriod {
		// Not started yet, or started recently
		return
	}

	clientMutationId := pod.GetName()
	rawJobUUID, exists := pod.GetLabels()[config.UUIDLabel]
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

	const (
		outcomeNothing = iota
		outcomeFail
		outcomeCancel
	)
	outcome := outcomeNothing
	images := make(map[string]struct{})

	for _, containerStatus := range pod.Status.InitContainerStatuses {
		if !shouldCancel(&containerStatus) {
			continue
		}
		if !isImagePullCheckContainer(&containerStatus) {
			log.Info("Ignoring init container during ImagePullBackOff watch.", zap.String("name", containerStatus.Name))
			continue
		}
		outcome = max(outcome, outcomeFail)
		images[containerStatus.Image] = struct{}{}
	}

	// These containers only run after the init containers have run.
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if !shouldCancel(&containerStatus) {
			continue
		}
		if !isSystemContainer(&containerStatus) {
			log.Info("Ignoring container during ImagePullBackOff watch.", zap.String("name", containerStatus.Name))
			continue
		}
		outcome = max(outcome, outcomeCancel)
		images[containerStatus.Image] = struct{}{}
	}

	switch outcome {
	case outcomeFail:
		log.Info("One or more image pull check containers are in ImagePullBackOff. Failing.")
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

		// Need the tags...
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
			return
		}

		// Let's also evict the pod (request graceful termination).
		eviction := &policyv1.Eviction{
			ObjectMeta: pod.ObjectMeta,
		}
		if err := w.k8s.PolicyV1().Evictions(w.cfg.Namespace).Evict(ctx, eviction); err != nil {
			log.Error("Couldn't evict pod", zap.Error(err))
		}

	case outcomeCancel:
		log.Info("One or more job containers are in ImagePullBackOff. Cancelling.")

		resp, err := api.GetCommandJob(ctx, w.gql, jobUUID.String())
		if err != nil {
			log.Warn("Failed to query command job", zap.Error(err))
			return
		}

		switch job := resp.GetJob().(type) {
		case *api.GetCommandJobJobJobTypeCommand:
			// This is expected as there will be a gap between when cancel request completes and
			// the Kubernetes job is cleaned up, during which more pods with containers destined to
			// ImagePullBackOff may be created.
			if job.GetState() == api.JobStatesCanceled || job.GetState() == api.JobStatesCanceling {
				return
			}

			if _, err := api.CancelCommandJob(ctx, w.gql, api.JobTypeCommandCancelInput{
				ClientMutationId: clientMutationId,
				Id:               job.GetId(),
			}); err != nil {
				log.Warn("Failed to cancel command job", zap.Error(err), zap.String("state", string(job.GetState())))
			}
			return
		default:
			log.Warn("Job was not a command job")
			return
		}
	}
}

func shouldCancel(containerStatus *corev1.ContainerStatus) bool {
	return containerStatus.State.Waiting != nil &&
		containerStatus.State.Waiting.Reason == "ImagePullBackOff"
}

func isImagePullCheckContainer(containerStatus *corev1.ContainerStatus) bool {
	return strings.HasPrefix(containerStatus.Name, ImagePullCheckContainerNamePrefix)
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
