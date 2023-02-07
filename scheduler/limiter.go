package scheduler

import (
	"context"
	"fmt"
	"sync"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
)

type MaxInFlightLimiter struct {
	scheduler   monitor.JobHandler
	MaxInFlight int
	k8s         kubernetes.Interface

	logger      *zap.Logger
	mu          sync.RWMutex
	inFlight    map[string]struct{}
	completions *sync.Cond
}

func NewLimiter(logger *zap.Logger, scheduler monitor.JobHandler, k8s kubernetes.Interface, maxInFlight int) *MaxInFlightLimiter {
	l := &MaxInFlightLimiter{
		scheduler:   scheduler,
		MaxInFlight: maxInFlight,
		logger:      logger,
		inFlight:    make(map[string]struct{}),
		k8s:         k8s,
	}
	l.completions = sync.NewCond(&l.mu)
	return l
}

// Creates a Jobs informer, registers the handler on it, and waits for cache sync
func (l *MaxInFlightLimiter) RegisterInformer(ctx context.Context, tags []string) error {
	hasTag, err := labels.NewRequirement(api.TagLabel, selection.In, api.TagsToLabels(tags))
	if err != nil {
		return fmt.Errorf("failed to build tag label selector for job manager: %w", err)
	}
	hasUUID, err := labels.NewRequirement(api.UUIDLabel, selection.Exists, nil)
	if err != nil {
		return fmt.Errorf("failed to build uuid label selector for job manager: %w", err)
	}
	factory := informers.NewSharedInformerFactoryWithOptions(l.k8s, 0, informers.WithTweakListOptions(func(opt *metav1.ListOptions) {
		opt.LabelSelector = labels.NewSelector().Add(*hasTag, *hasUUID).String()
	}))
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(l); err != nil {
		return fmt.Errorf("failed to register job event handler: %w", err)
	}

	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

func (l *MaxInFlightLimiter) Create(ctx context.Context, job *monitor.Job) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return l.add(ctx, job)
	}
}

func (l *MaxInFlightLimiter) add(ctx context.Context, job *monitor.Job) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, found := l.inFlight[job.Uuid]; found {
		l.logger.Debug("skipping already queued job", zap.String("uuid", job.Uuid))
		return nil
	}
	inFlight := len(l.inFlight)
	if l.MaxInFlight > 0 && inFlight >= l.MaxInFlight {
		l.logger.Debug("max-in-flight reached", zap.Int("in-flight", inFlight))
		l.completions.Wait()
	}
	if err := l.scheduler.Create(ctx, job); err != nil {
		return err
	}
	l.inFlight[job.Uuid] = struct{}{}
	return nil
}

// load jobs at controller startup/restart
func (l *MaxInFlightLimiter) OnAdd(obj interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	job, ok := obj.(*v1.Pod)
	if ok && !isFinished(job) {
		uuid := job.Labels[api.UUIDLabel]
		if _, alreadyInFlight := l.inFlight[uuid]; !alreadyInFlight {
			l.logger.Debug("adding in-flight job", zap.String("uuid", uuid), zap.Int("in-flight", len(l.inFlight)))
			l.inFlight[uuid] = struct{}{}
		}
	}
}

// if a job is still running, add it to inFlight, otherwise try to remove it
func (l *MaxInFlightLimiter) OnUpdate(_, obj interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	job := obj.(*v1.Pod)
	uuid := job.Labels[api.UUIDLabel]
	if isFinished(job) {
		l.markComplete(job)
	} else {
		if _, alreadyInFlight := l.inFlight[uuid]; !alreadyInFlight {
			l.logger.Debug("waiting for job completion", zap.String("uuid", uuid))
			l.inFlight[uuid] = struct{}{}
		}
	}
}

// if jobs are deleted before they complete, ensure we remove them from inFlight
func (l *MaxInFlightLimiter) OnDelete(obj interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.markComplete(obj.(*v1.Pod))
}

func (l *MaxInFlightLimiter) markComplete(pod *v1.Pod) {
	uuid := pod.Labels[api.UUIDLabel]
	if _, alreadyInFlight := l.inFlight[uuid]; alreadyInFlight {
		delete(l.inFlight, uuid)
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			job, err := l.k8s.BatchV1().Jobs(pod.Namespace).Get(context.TODO(), pod.Labels["job-name"], metav1.GetOptions{})
			if err != nil {
				return err
			} else {
				job.Spec.ActiveDeadlineSeconds = pointer.Int64(1)
				_, err = l.k8s.BatchV1().Jobs(pod.Namespace).Update(context.TODO(), job, metav1.UpdateOptions{})
				return err
			}
		}); err != nil {
			l.logger.Error("failed to update job", zap.Error(err))
		}
		l.logger.Debug("job complete", zap.String("uuid", uuid), zap.Int("in-flight", len(l.inFlight)))
		l.completions.Signal()
	}
}

func isFinished(job *v1.Pod) bool {
	for _, container := range job.Status.ContainerStatuses {
		if container.Name == AgentContainerName {
			if container.State.Terminated != nil {
				return true
			}
		}
	}
	return false
}
