package deduper

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// Deduper is a job handler that wraps another job handler (typically Limiter)
// and only creates a new job if an existing job does not already exist.
type Deduper struct {
	// Next handler in the chain.
	handler model.JobHandler

	// Logs go here
	logger *zap.Logger

	// Map to track in-flight jobs, and mutex to protect it.
	inFlightMu sync.Mutex
	inFlight   map[uuid.UUID]bool
}

// New creates a Deduper.
func New(logger *zap.Logger, handler model.JobHandler) *Deduper {
	l := &Deduper{
		handler:  handler,
		logger:   logger,
		inFlight: make(map[uuid.UUID]bool),
	}
	return l
}

// RegisterInformer registers the limiter to listen for Kubernetes job events,
// and waits for cache sync.
func (d *Deduper) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	if _, err := jobInformer.AddEventHandler(d); err != nil {
		return err
	}
	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

// Handle passes the job to the next handler if the job is not already
// scheduled. Otherwise, it returns [model.ErrDuplicateJob].
func (d *Deduper) Handle(ctx context.Context, job model.Job) error {
	uuid, err := uuid.Parse(job.Uuid)
	if err != nil {
		d.logger.Error("invalid UUID in CommandJob", zap.Error(err))
		return err
	}
	if numInFlight, ok := d.casa(uuid, true); !ok {
		d.logger.Debug("job is already in-flight",
			zap.String("uuid", job.Uuid),
			zap.Int("num-in-flight", numInFlight),
		)
		return model.ErrDuplicateJob
	}

	// Not a duplicate: pass to the next handler, which could be either the
	// limiter or the scheudler.
	d.logger.Debug("passing job to next handler",
		zap.Stringer("handler", reflect.TypeOf(d.handler)),
		zap.String("uuid", job.Uuid),
	)
	if err := d.handler.Handle(ctx, job); err != nil {
		// Couldn't schedule the job. Oh well. Record as not-in-flight.
		numInFlight, _ := d.casa(uuid, false)

		d.logger.Debug("next handler failed",
			zap.String("uuid", job.Uuid),
			zap.Int("num-in-flight", numInFlight),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// OnAdd is called by k8s to inform us a resource is added.
func (d *Deduper) OnAdd(obj any, _ bool) {
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	d.trackJob(job)
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (d *Deduper) OnUpdate(_, obj any) {
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	d.trackJob(job)
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (d *Deduper) OnDelete(obj any) {
	// The job condition at the point of deletion could be non-terminal, so
	// we ignore it and skip to marking complete.
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	id, err := uuid.Parse(job.Labels[config.UUIDLabel])
	if err != nil {
		d.logger.Error("invalid UUID in job label", zap.Error(err))
		return
	}
	d.markComplete(id)
}

// trackJob is called by the k8s informer callbacks to update job state.
func (d *Deduper) trackJob(job *batchv1.Job) {
	id, err := uuid.Parse(job.Labels[config.UUIDLabel])
	if err != nil {
		d.logger.Error("invalid UUID in job label", zap.Error(err))
		return
	}
	if model.JobFinished(job) {
		d.markComplete(id)
	} else {
		d.markRunning(id)
	}
}

// markRunning records a job as in-flight.
func (d *Deduper) markRunning(id uuid.UUID) {
	// Change state from not in-flight to in-flight.
	numInFlight, ok := d.casa(id, true)
	if !ok {
		d.logger.Debug("markRunning: job was already in-flight!",
			zap.String("uuid", id.String()),
			zap.Int("num-in-flight", numInFlight),
		)
		return
	}

	d.logger.Debug(
		"markRunning: added previously unknown in-flight job",
		zap.String("uuid", id.String()),
		zap.Int("num-in-flight", numInFlight),
	)
}

// markComplete records a job as not in-flight.
func (d *Deduper) markComplete(id uuid.UUID) {
	// Change state from in-flight to not in-flight.
	numInFlight, ok := d.casa(id, false)
	if !ok {
		d.logger.Debug("markComplete: job was already not-in-flight!",
			zap.String("uuid", id.String()),
			zap.Int("num-in-flight", numInFlight),
		)
		return
	}

	d.logger.Debug("markComplete: job complete",
		zap.String("uuid", id.String()),
		zap.Int("num-in-flight", numInFlight),
	)
}

// casa is an atomic compare-and-swap-like primitive.
//
// It attempts to update the state of the job from !x to x, and reports
// the in-flight count (after the operation) and whether it was able to change
// the state, i.e. it returns false if the in-flight state of the job was
// already equal to x.
func (d *Deduper) casa(id uuid.UUID, x bool) (int, bool) {
	d.inFlightMu.Lock()
	defer d.inFlightMu.Unlock()
	if d.inFlight[id] == x {
		return len(d.inFlight), false
	}
	if x {
		d.inFlight[id] = true
	} else {
		delete(d.inFlight, id)
	}
	return len(d.inFlight), true
}
