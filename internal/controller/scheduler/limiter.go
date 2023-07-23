package scheduler

import (
	"context"
	"fmt"
	"sync"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type MaxInFlightLimiter struct {
	scheduler   monitor.JobHandler
	MaxInFlight int

	logger      *zap.Logger
	mu          sync.RWMutex
	inFlight    map[string]struct{}
	completions *sync.Cond
}

func NewLimiter(
	logger *zap.Logger,
	scheduler monitor.JobHandler,
	maxInFlight int,
) *MaxInFlightLimiter {
	l := &MaxInFlightLimiter{
		scheduler:   scheduler,
		MaxInFlight: maxInFlight,
		logger:      logger,
		inFlight:    make(map[string]struct{}),
	}
	l.completions = sync.NewCond(&l.mu)
	return l
}

// Creates a Jobs informer, registers the handler on it, and waits for cache sync
func (l *MaxInFlightLimiter) RegisterInformer(
	ctx context.Context,
	factory informers.SharedInformerFactory,
) error {
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	jobInformer.AddEventHandler(l)
	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

func (l *MaxInFlightLimiter) Create(ctx context.Context, job *api.CommandJob) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return l.add(ctx, job)
	}
}

func (l *MaxInFlightLimiter) add(ctx context.Context, job *api.CommandJob) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, found := l.inFlight[job.Uuid]; found {
		l.logger.Debug("skipping already queued job", zap.String("uuid", job.Uuid))
		return nil
	}
	for l.MaxInFlight > 0 && len(l.inFlight) >= l.MaxInFlight {
		l.logger.Debug("max-in-flight reached", zap.Int("in-flight", len(l.inFlight)))
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

	job := obj.(*batchv1.Job)
	if !jobFinished(job) {
		uuid := job.Labels[config.UUIDLabel]
		if _, alreadyInFlight := l.inFlight[uuid]; !alreadyInFlight {
			l.logger.Debug(
				"adding in-flight job",
				zap.String("uuid", uuid),
				zap.Int("in-flight", len(l.inFlight)),
			)
			l.inFlight[uuid] = struct{}{}
		}
	}
}

// if a job is still running, add it to inFlight, otherwise try to remove it
func (l *MaxInFlightLimiter) OnUpdate(_, obj interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	job := obj.(*batchv1.Job)
	uuid := job.Labels[config.UUIDLabel]
	if jobFinished(job) {
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

	l.markComplete(obj.(*batchv1.Job))
}

func (l *MaxInFlightLimiter) markComplete(job *batchv1.Job) {
	uuid := job.Labels[config.UUIDLabel]
	if _, alreadyInFlight := l.inFlight[uuid]; alreadyInFlight {
		delete(l.inFlight, uuid)
		l.logger.Debug(
			"job complete",
			zap.String("uuid", uuid),
			zap.Int("in-flight", len(l.inFlight)),
		)
		l.completions.Signal()
	}
}

func (l *MaxInFlightLimiter) InFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.inFlight)
}

func jobFinished(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		switch cond.Type {
		case batchv1.JobComplete, batchv1.JobFailed:
			return true
		}
	}
	return false
}
