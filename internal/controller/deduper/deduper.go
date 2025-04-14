package deduper

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/buildkite/agent-stack-k8s/v2/api"
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
	d := &Deduper{
		handler:  handler,
		logger:   logger,
		inFlight: make(map[uuid.UUID]bool),
	}
	// Provide the callback for numInFlightGauge.
	jobsRunningGaugeFunc = func() int {
		d.inFlightMu.Lock()
		defer d.inFlightMu.Unlock()
		return len(d.inFlight)
	}
	return d
}

// RegisterInformer registers the limiter to listen for Kubernetes job events,
// and waits for cache sync.
func (d *Deduper) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	reg, err := jobInformer.AddEventHandler(d)
	if err != nil {
		return err
	}
	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), reg.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}

// Handle passes the job to the next handler if the job is not already
// scheduled. Otherwise, it returns [model.ErrDuplicateJob].
func (d *Deduper) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	uuid, err := uuid.Parse(job.ID)
	if err != nil {
		d.logger.Error("invalid UUID in CommandJob", zap.Error(err))
		return err
	}
	if numInFlight, ok := d.casa(uuid, true); !ok {
		jobsAlreadyRunningCounter.WithLabelValues("Handle").Inc()
		d.logger.Debug("job is already in-flight",
			zap.String("job-uuid", job.ID),
			zap.Int("num-in-flight", numInFlight),
		)
		return model.ErrDuplicateJob
	}
	jobsMarkedRunningCounter.WithLabelValues("Handle").Inc()

	// Not a duplicate: pass to the next handler, which could be either the
	// limiter or the scheudler.
	d.logger.Debug("passing job to next handler",
		zap.Stringer("handler", reflect.TypeOf(d.handler)),
		zap.String("job-uuid", job.ID),
	)
	jobHandlerCallsCounter.Inc()

	if err := d.handler.Handle(ctx, job); err != nil {
		jobHandlerErrorCounter.Inc()

		if errors.Is(err, model.ErrDuplicateJob) {
			// It already exists, despite our efforts to deduplicate it?
			// Leave it marked as running then - just return.
			return err
		}

		// Couldn't schedule the job, it's not a duplicate. Oh well.
		// Record as not-in-flight.
		numInFlight, ok := d.casa(uuid, false)
		if ok {
			jobsUnmarkedRunningCounter.WithLabelValues("Handle").Inc()
		} else {
			jobsAlreadyNotRunningCounter.WithLabelValues("Handle").Inc()
		}

		d.logger.Debug("next handler failed",
			zap.String("job-uuid", job.ID),
			zap.Int("num-in-flight", numInFlight),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// OnAdd is called by k8s to inform us a resource is added.
func (d *Deduper) OnAdd(obj any, inInitialList bool) {
	onAddEventCounter.Inc()

	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}

	// Once the initial list is complete, we are told about new jobs before they
	// are created in Handle.
	if !inInitialList {
		return
	}
	id, err := uuid.Parse(job.Labels[config.UUIDLabel])
	if err != nil {
		d.logger.Error("invalid UUID in job label", zap.Error(err))
		return
	}

	// Change state from not in-flight to in-flight.
	numInFlight, ok := d.casa(id, true)
	if !ok {
		jobsAlreadyRunningCounter.WithLabelValues("OnAdd").Inc()
		d.logger.Debug("job was already in inFlight!",
			zap.String("uuid", id.String()),
			zap.Int("num-in-flight", numInFlight),
		)
		return
	}

	jobsMarkedRunningCounter.WithLabelValues("OnAdd").Inc()
	d.logger.Debug("added previous job to inFlight",
		zap.String("uuid", id.String()),
		zap.Int("num-in-flight", numInFlight),
	)
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (d *Deduper) OnUpdate(any, any) {
	onUpdateEventCounter.Inc()
	// Otherwise ignore this event. Although this can tell us that a job has
	// gone from running to finished, the Job continues to exist in the cluster
	// until it is cleaned up through ttlSecondsAfterFinished. Trying to
	// create the same job again will fail.
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (d *Deduper) OnDelete(prev any) {
	onDeleteEventCounter.Inc()

	prevState, _ := prev.(*batchv1.Job)
	if prevState == nil {
		return
	}
	// Whether or not the job had become terminal, it's now deleted.
	id, err := uuid.Parse(prevState.Labels[config.UUIDLabel])
	if err != nil {
		d.logger.Error("invalid UUID in job label", zap.Error(err))
		return
	}

	// Change state from in-flight to not in-flight.
	numInFlight, ok := d.casa(id, false)
	if !ok {
		d.logger.Debug("job was already missing from inFlight!",
			zap.String("job-uuid", id.String()),
			zap.Int("num-in-flight", numInFlight),
		)
		jobsAlreadyNotRunningCounter.WithLabelValues("OnDelete").Inc()
		return
	}

	d.logger.Debug("job removed from inFlight",
		zap.String("job-uuid", id.String()),
		zap.Int("num-in-flight", numInFlight),
	)
	jobsUnmarkedRunningCounter.WithLabelValues("OnDelete").Inc()
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
