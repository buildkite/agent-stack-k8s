package scheduler

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/uuid"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/tools/cache"
)

type imagePullBackOffWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface
	gql    graphql.Client
}

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
	w.logger.Info("registering imagePullBackOffWatcher with informer")
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return fmt.Errorf("failed to register pod event handler: %w", err)
	}

	w.logger.Info("starting imagePullBackOffWatcher")
	go factory.Start(ctx.Done())
	return nil
}

// ignored
func (w *imagePullBackOffWatcher) OnDelete(obj any) {}

// handle pods completed while the controller wasn't running
func (w *imagePullBackOffWatcher) OnAdd(obj any) {
	pod, _ := obj.(*v1.Pod)
	w.cancelImagePullBackOff(context.Background(), pod)
}

func (w *imagePullBackOffWatcher) OnUpdate(old, new any) {
	oldPod, _ := old.(*v1.Pod)
	if terminated := getTermination(oldPod); terminated != nil {
		// skip subsequent reconciles after we've already handled termination
		return
	}

	newPod, _ := new.(*v1.Pod)
	w.cancelImagePullBackOff(context.Background(), newPod)
}

func (w *imagePullBackOffWatcher) cancelImagePullBackOff(ctx context.Context, pod *v1.Pod) {
	log := w.logger.With(zap.String("namespace", pod.Namespace), zap.String("podName", pod.Name))
	log.Info("Checking pod for ImagePullBackOff")

	clientMutationId := uuid.New()
	jobUUID, exists := pod.GetLabels()[api.UUIDLabel]
	if !exists {
		log.Info("Job UUID label not present")
		return
	}

	for _, containerStatus := range pod.Status.ContainerStatuses {
		if shouldCancel(&containerStatus) {
			log.Info("Job exceeded ImagePullBackOff Limit. Cancelling")
			if _, err := api.CommandJobCancel(ctx, w.gql, api.JobTypeCommandCancelInput{
				ClientMutationId: clientMutationId.String(),
				Id:               jobUUID,
			}); err != nil {
				log.Warn("Failed to cancel job")
			}
			return
		}
	}
}

func shouldCancel(containerStatus *v1.ContainerStatus) bool {
	return containerStatus.State.Waiting != nil &&
		containerStatus.State.Waiting.Reason == "ImagePullBackOff"
}
