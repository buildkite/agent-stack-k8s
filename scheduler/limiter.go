package scheduler

import (
	"context"
	"fmt"
	"sync"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type MaxInFlightLimiter struct {
	Input       <-chan monitor.Job
	Output      chan<- monitor.Job
	MaxInFlight int

	logger      *zap.Logger
	mu          sync.RWMutex
	inFlight    map[string]struct{}
	completions chan struct{}
}

func NewLimiter(logger *zap.Logger, input <-chan monitor.Job, output chan<- monitor.Job, maxInFlight int) *MaxInFlightLimiter {
	return &MaxInFlightLimiter{
		Input:       input,
		Output:      output,
		MaxInFlight: maxInFlight,
		logger:      logger,
		inFlight:    make(map[string]struct{}),
		completions: make(chan struct{}, maxInFlight),
	}
}

// Creates a Jobs informer, registers the handler on it, and waits for cache sync
func RegisterInformer(ctx context.Context, clientset kubernetes.Interface, tags []string, handler cache.ResourceEventHandler) error {
	hasTag, err := labels.NewRequirement(api.TagLabel, selection.In, api.TagsToLabels(tags))
	if err != nil {
		return fmt.Errorf("failed to build tag label selector for job manager: %w", err)
	}
	hasUUID, err := labels.NewRequirement(api.UUIDLabel, selection.Exists, nil)
	if err != nil {
		return fmt.Errorf("failed to build uuid label selector for job manager: %w", err)
	}
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithTweakListOptions(func(opt *metav1.ListOptions) {
		opt.LabelSelector = labels.NewSelector().Add(*hasTag, *hasUUID).String()
	}))
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	if _, err := jobInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("failed to register event handler: %w", err)
	}

	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

func (l *MaxInFlightLimiter) Run(ctx context.Context) {
	for {
		l.mu.RLock()
		inFlight := len(l.inFlight)
		l.mu.RUnlock()
		if l.MaxInFlight > 0 && inFlight >= l.MaxInFlight {
			<-l.completions // wait for a completion
			continue
		}

		select {
		case <-ctx.Done():
			return
		case job := <-l.Input:
			l.add(&job)
		}
	}
}

func (l *MaxInFlightLimiter) add(job *monitor.Job) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if job.Err != nil {
		l.Output <- *job
		return
	}
	if _, found := l.inFlight[job.Uuid]; found {
		l.logger.Debug("skipping already queued job", zap.String("uuid", job.Uuid))
		return
	}
	l.inFlight[job.Uuid] = struct{}{}
	l.Output <- *job
}

// ignored
func (l *MaxInFlightLimiter) OnAdd(obj interface{}) {}

// if a job is still running, add it to inFlight, otherwise try to remove it
func (l *MaxInFlightLimiter) OnUpdate(_, obj interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	job := obj.(*batchv1.Job)
	uuid := job.Labels[api.UUIDLabel]
	if isFinished(job) {
		l.markComplete(job)
	} else {
		l.logger.Debug("waiting for job completion", zap.String("uuid", uuid))
		l.inFlight[uuid] = struct{}{}
	}
}

// if jobs are deleted before they complete, ensure we remove them from inFlight
func (l *MaxInFlightLimiter) OnDelete(obj interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.markComplete(obj.(*batchv1.Job))
}

func (l *MaxInFlightLimiter) markComplete(job *batchv1.Job) {
	uuid := job.Labels[api.UUIDLabel]
	l.logger.Debug("job complete", zap.String("uuid", uuid))
	delete(l.inFlight, uuid)
}

func isFinished(job *batchv1.Job) bool {
	var finished bool
	for _, cond := range job.Status.Conditions {
		switch cond.Type {
		case batchv1.JobComplete, batchv1.JobFailed:
			finished = true
		}
	}
	return finished
}
