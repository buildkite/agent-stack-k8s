package scheduler

import (
	"context"
	"fmt"
	"sync"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"

	"github.com/google/uuid"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// MaxInFlightLimiter is a job handler that wraps another job handler
// (typically the actual job scheduler) and only creates new jobs if the total
// number of jobs currently running is below a limit.
type MaxInFlightLimiter struct {
	// MaxInFlight sets the upper limit on number of jobs running concurrently
	// in the cluster. 0 means no limit.
	MaxInFlight int

	// scheduler is the thing that actually schedules jobs in the cluster.
	scheduler monitor.JobHandler

	// Logs go here
	logger *zap.Logger

	// Map to track in-flight jobs, and mutex to protect it.
	inFlightMu sync.Mutex
	inFlight   map[uuid.UUID]bool

	// When a job starts, it takes a token from the bucket.
	// When a job ends, it puts a token back in the bucket.
	tokenBucket chan struct{}
}

// NewLimiter creates a MaxInFlightLimiter.
func NewLimiter(logger *zap.Logger, scheduler monitor.JobHandler, maxInFlight int) *MaxInFlightLimiter {
	l := &MaxInFlightLimiter{
		scheduler:   scheduler,
		MaxInFlight: maxInFlight,
		logger:      logger,
		inFlight:    make(map[uuid.UUID]bool),
		tokenBucket: make(chan struct{}, maxInFlight),
	}
	if maxInFlight <= 0 { // infinite capacity
		// All receives from a closed channel succeed immediately.
		close(l.tokenBucket)
	}
	for range maxInFlight { // finite capacity
		// Fill the bucket with tokens.
		l.tokenBucket <- struct{}{}
	}
	return l
}

// RegisterInformer registers the limiter to listen for Kubernetes job events,
// and waits for cache sync.
func (l *MaxInFlightLimiter) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	if _, err := jobInformer.AddEventHandler(l); err != nil {
		return err
	}
	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

// Create either creates the job immediately, or blocks until there is capacity.
// It may also ignore the job if it is already in flight.
func (l *MaxInFlightLimiter) Create(ctx context.Context, job *api.CommandJob) error {
	uuid, err := uuid.Parse(job.Uuid)
	if err != nil {
		l.logger.Error("invalid UUID in CommandJob", zap.Error(err))
		return err
	}
	if numInFlight, ok := l.casa(uuid, true); !ok {
		l.logger.Debug("Create: job is already in-flight",
			zap.String("uuid", job.Uuid),
			zap.Int("num-in-flight", numInFlight),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
		return nil
	}

	// Block until there's a token in the bucket.
	select {
	case <-ctx.Done():
		return context.Cause(ctx)

	case <-l.tokenBucket:
		l.logger.Debug("Create: token acquired",
			zap.String("uuid", uuid.String()),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
	}

	// We got a token from the bucket above! Proceed to schedule the pod.
	if err := l.scheduler.Create(ctx, job); err != nil {
		// Oh well. Return the token and un-record the job.
		l.tryReturnToken()
		numInFlight, _ := l.casa(uuid, false)

		l.logger.Debug("Create: scheduler failed to enqueue job",
			zap.String("uuid", job.Uuid),
			zap.Int("num-in-flight", numInFlight),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
		return err
	}
	return nil
}

// OnAdd is called by k8s to inform us a resource is added.
func (l *MaxInFlightLimiter) OnAdd(obj any, _ bool) {
	l.trackJob(obj.(*batchv1.Job))
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (l *MaxInFlightLimiter) OnUpdate(_, obj any) {
	l.trackJob(obj.(*batchv1.Job))
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (l *MaxInFlightLimiter) OnDelete(obj any) {
	// The job condition at the point of deletion could be non-terminal, so
	// we ignore it and skip to marking complete.
	job := obj.(*batchv1.Job)
	id, err := uuid.Parse(job.Labels[config.UUIDLabel])
	if err != nil {
		l.logger.Error("invalid UUID in job label", zap.Error(err))
		return
	}
	l.markComplete(id)
}

// trackJob is called by the k8s informer callbacks to update job state and
// take/return tokens. It does the same thing for all three callbacks because
func (l *MaxInFlightLimiter) trackJob(job *batchv1.Job) {
	id, err := uuid.Parse(job.Labels[config.UUIDLabel])
	if err != nil {
		l.logger.Error("invalid UUID in job label", zap.Error(err))
		return
	}
	if jobFinished(job) {
		l.markComplete(id)
	} else {
		l.markRunning(id)
	}
}

// markRunning records a job as in-flight.
func (l *MaxInFlightLimiter) markRunning(id uuid.UUID) {
	// Change state from not in-flight to in-flight.
	numInFlight, ok := l.casa(id, true)
	if !ok {
		l.logger.Debug("markRunning: job is already in-flight",
			zap.String("uuid", id.String()),
			zap.Int("num-in-flight", numInFlight),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
		return
	}

	// It wasn't recorded as in-flight before, but is now. So it wasn't started
	// by this instance of the controller (probably a previous instance).
	// Try to take a token from the bucket to keep it in balance. But because
	// this job is already running, we don't block waiting for one.
	l.tryTakeToken()

	l.logger.Debug(
		"markRunning: added previously unknown in-flight job",
		zap.String("uuid", id.String()),
		zap.Int("num-in-flight", numInFlight),
		zap.Int("available-tokens", len(l.tokenBucket)),
	)
}

// markComplete records a job as not in-flight.
func (l *MaxInFlightLimiter) markComplete(id uuid.UUID) {
	// Change state from in-flight to not in-flight.
	numInFlight, ok := l.casa(id, false)
	if !ok {
		l.logger.Debug("markComplete: job was already not in-flight",
			zap.String("uuid", id.String()),
			zap.Int("num-in-flight", numInFlight),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
		return
	}

	// It was previously recorded as in-flight, now it is not. So we can
	// return a token to the bucket. But we shouldn't block trying to do that:
	// this may have been a job started by a previous instance of the controller
	// with a higher MaxInFlight than this instance.
	l.tryReturnToken()

	l.logger.Debug("markComplete: job complete",
		zap.String("uuid", id.String()),
		zap.Int("num-in-flight", numInFlight),
		zap.Int("available-tokens", len(l.tokenBucket)),
	)
}

// jobFinished reports if the job has a Complete or Failed status condition.
func jobFinished(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		switch cond.Type {
		case batchv1.JobComplete, batchv1.JobFailed:
			// Per the API docs, these are the only terminal job conditions.
			return true
		}
	}
	return false
}

// tryTakeToken takes a token from the bucket, if one is available. It does not
// block.
func (l *MaxInFlightLimiter) tryTakeToken() {
	select {
	case <-l.tokenBucket:
	default:
	}
}

// tryReturnToken returns a token to the bucket, if not closed or full. It does
// not block.
func (l *MaxInFlightLimiter) tryReturnToken() {
	if l.MaxInFlight <= 0 {
		return
	}
	select {
	case l.tokenBucket <- struct{}{}:
	default:
	}
}

// casa is an atomic compare-and-swap-like primitive.
//
// It attempts to update the state of the job from !x to x, and reports
// the in-flight count (after the operation) and whether it was able to change
// the state, i.e. it returns false if the in-flight state of the job was
// already equal to x.
func (l *MaxInFlightLimiter) casa(id uuid.UUID, x bool) (int, bool) {
	l.inFlightMu.Lock()
	defer l.inFlightMu.Unlock()
	if l.inFlight[id] == x {
		return len(l.inFlight), false
	}
	if x {
		l.inFlight[id] = true
	} else {
		delete(l.inFlight, id)
	}
	return len(l.inFlight), true
}
