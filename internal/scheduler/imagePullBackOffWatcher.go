package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/uuid"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
)

const gracePeriodSeconds = 30

type imagePullBackOffWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface
	gql    graphql.Client
}

// NewImagePullBackOffWatcher creates an informer that will use the Buildkite
// GraphQL API to cancel jobs that have pods with containers in the
// ImagePullBackOff state
func NewImagePullBackOffWatcher(
	logger *zap.Logger,
	k8s kubernetes.Interface,
	cfg api.Config,
) *imagePullBackOffWatcher {
	return &imagePullBackOffWatcher{
		logger: logger,
		k8s:    k8s,
		gql:    api.NewClient(cfg.BuildkiteToken),
	}
}

// Creates a Pods informer and registers the handler on it
func (w *imagePullBackOffWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return fmt.Errorf("failed to register pod event handler: %w", err)
	}
	go factory.Start(ctx.Done())
	return nil
}

func (w *imagePullBackOffWatcher) OnDelete(obj any) {}

func (w *imagePullBackOffWatcher) OnAdd(maybePod any) {
	pod, wasPod := maybePod.(*v1.Pod)
	if !wasPod {
		return
	}

	w.cancelImagePullBackOff(context.Background(), pod)
}

func (w *imagePullBackOffWatcher) OnUpdate(oldMaybePod, newMaybePod any) {
	oldPod, oldWasPod := newMaybePod.(*v1.Pod)
	newPod, newWasPod := newMaybePod.(*v1.Pod)

	// This nonsense statement is only necessary because the types are too loose.
	// Most likely both old and new are going to be Pods.
	if newWasPod {
		w.cancelImagePullBackOff(context.Background(), newPod)
	} else if oldWasPod {
		w.cancelImagePullBackOff(context.Background(), oldPod)
	}
}

func (w *imagePullBackOffWatcher) cancelImagePullBackOff(ctx context.Context, pod *v1.Pod) {
	log := w.logger.With(zap.String("namespace", pod.Namespace), zap.String("podName", pod.Name))
	log.Debug("Checking pod for ImagePullBackOff")

	clientMutationId := pod.GetName()
	rawJobUUID, exists := pod.GetLabels()[api.UUIDLabel]
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
	startedAt := pod.GetCreationTimestamp().Time

	for _, containerStatus := range pod.Status.ContainerStatuses {
		if shouldCancel(&containerStatus, startedAt) {
			log.Info("Job has a container in ImagePullBackOff. Cancelling.")

			resp, err := api.GetCommandJob(ctx, w.gql, jobUUID.String())
			if err != nil {
				log.Warn("Failed to query command job", zap.Error(err))
				return
			}

			switch job := resp.GetJob().(type) {
			case *api.GetCommandJobJobJobTypeCommand:
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
}

func shouldCancel(containerStatus *v1.ContainerStatus, startedAt time.Time) bool {
	return containerStatus.State.Waiting != nil &&
		containerStatus.State.Waiting.Reason == "ImagePullBackOff" &&
		time.Since(startedAt) > gracePeriodSeconds*time.Second
}
