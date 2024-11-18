package limiter

import (
	"context"
	"fmt"
	"reflect"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// MaxInFlight is a job handler that wraps another job handler
// (typically the actual job scheduler) and only creates new jobs if the total
// number of jobs currently running is below a limit.
type MaxInFlight struct {
	// MaxInFlight sets the upper limit on number of jobs running concurrently
	// in the cluster. 0 means no limit.
	MaxInFlight int

	// Next handler in the chain.
	handler model.JobHandler

	// Logs go here
	logger *zap.Logger

	// When a job starts, it takes a token from the bucket.
	// When a job ends, it puts a token back in the bucket.
	tokenBucket chan struct{}
}

// New creates a MaxInFlight limiter. maxInFlight must be at least 1.
func New(logger *zap.Logger, scheduler model.JobHandler, maxInFlight int) *MaxInFlight {
	if maxInFlight <= 0 {
		// Using panic, because getting here is severe programmer error and the
		// whole controller is still just starting up.
		panic(fmt.Sprintf("maxInFlight <= 0 (got %d)", maxInFlight))
	}
	l := &MaxInFlight{
		handler:     scheduler,
		MaxInFlight: maxInFlight,
		logger:      logger,
		tokenBucket: make(chan struct{}, maxInFlight),
	}
	for range maxInFlight {
		// Fill the bucket with tokens.
		l.tokenBucket <- struct{}{}
	}
	return l
}

// RegisterInformer registers the limiter to listen for Kubernetes job events,
// and waits for cache sync.
func (l *MaxInFlight) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
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

// Handle either passes the job onto the next handler immediately, or blocks
// until there is capacity. It returns [model.ErrStaleJob] if the job data
// becomes too stale while waiting for capacity.
func (l *MaxInFlight) Handle(ctx context.Context, job model.Job) error {
	// Block until there's a token in the bucket, or cancel if the job
	// information becomes too stale.
	select {
	case <-ctx.Done():
		return context.Cause(ctx)

	case <-job.StaleCh:
		return model.ErrStaleJob

	case <-l.tokenBucket:
		l.logger.Debug("token acquired",
			zap.String("uuid", job.Uuid),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
	}

	// We got a token from the bucket above! Proceed to schedule the pod.
	// The next handler should be Scheduler (except in some tests).
	l.logger.Debug("passing job to next handler",
		zap.Stringer("handler", reflect.TypeOf(l.handler)),
		zap.String("uuid", job.Uuid),
	)
	if err := l.handler.Handle(ctx, job); err != nil {
		// Oh well. Return the token and un-record the job.
		l.tryReturnToken()

		l.logger.Debug("next handler failed",
			zap.String("uuid", job.Uuid),
			zap.Int("available-tokens", len(l.tokenBucket)),
		)
		return err
	}
	return nil
}

// OnAdd is called by k8s to inform us a resource is added.
func (l *MaxInFlight) OnAdd(obj any, _ bool) {
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	l.trackJob(job)
	l.logger.Debug("at end of OnAdd", zap.Int("tokens-available", len(l.tokenBucket)))
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (l *MaxInFlight) OnUpdate(_, obj any) {
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	l.trackJob(job)
	l.logger.Debug("at end of OnUpdate", zap.Int("tokens-available", len(l.tokenBucket)))
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (l *MaxInFlight) OnDelete(obj any) {
	// The job condition at the point of deletion could be non-terminal, but
	// it is being deleted, so ignore it and skip to marking complete.
	// If buildkite.com/job-uuid label is missing or malformed, don't track it.
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	l.trackJob(job)
	if _, err := uuid.Parse(job.Labels[config.UUIDLabel]); err != nil {
		return
	}
	l.tryReturnToken()
	l.logger.Debug("at end of OnDelete", zap.Int("tokens-available", len(l.tokenBucket)))
}

// trackJob is called by the k8s informer callbacks to update job state and
// take/return tokens. It does the same thing for all three callbacks.
func (l *MaxInFlight) trackJob(job *batchv1.Job) {
	// If buildkite.com/job-uuid label is missing or malformed, don't track it.
	if _, err := uuid.Parse(job.Labels[config.UUIDLabel]); err != nil {
		return
	}

	if model.JobFinished(job) {
		l.tryReturnToken()
	} else {
		l.tryTakeToken()
	}
}

// tryTakeToken takes a token from the bucket, if one is available. It does not
// block.
func (l *MaxInFlight) tryTakeToken() {
	select {
	case <-l.tokenBucket:
	default:
	}
}

// tryReturnToken returns a token to the bucket, if not full. It does not block.
func (l *MaxInFlight) tryReturnToken() {
	select {
	case l.tokenBucket <- struct{}{}:
	default:
	}
}
