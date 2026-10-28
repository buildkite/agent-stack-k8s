package limiter

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"log/slog"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// Limiter manages a queue of jobs. While there are jobs available and less
// than the MaxInFlight limit running, it will take jobs from the queue to pass
// the next handler in the chain.
type Limiter struct {
	// MaxInFlight sets the upper limit on number of jobs running concurrently
	// in the cluster. Zero means unlimited.
	MaxInFlight int

	// Controls the number of workers that pass along jobs.
	JobCreationConcurrency int

	// Maximum number of jobs to store in the work queue.
	WorkQueueLimit int

	// Next handler in the chain.
	handler model.JobHandler

	// Logs go here
	logger *slog.Logger

	// Closed and replaced under mu to notify every worker of state changes.
	newWork chan struct{}

	// Queue and job accounting share a lock: checking capacity, dequeuing a
	// job, and occupying its slot must be atomic with informer notifications.
	mu       sync.Mutex
	queue    []*api.AgentScheduledJob
	paused   bool
	stopping bool
	jobs     map[string]*jobState
	inFlight int

	// Intake follows the controller context; accepted dispatch survives it
	// until Drain's deadline. Workers finish at resource creation, not job completion.
	intakeCtx      context.Context
	dispatchCtx    context.Context
	cancelDispatch context.CancelFunc
	workersDone    chan struct{}
	workerWG       sync.WaitGroup
}

// A scheduling attempt occupies a slot before Kubernetes reports the job.
// Once observed, the job keeps its slot even if the attempt returns an error.
// Finished jobs occupy no slot, but remain known until deletion so that a
// repeated poll cannot schedule them again while they still exist.
type jobState struct {
	observed bool
	finished bool
}

// New creates a Limiter. maxInFlight must be non-negative, but 0 is interpreted
// as no limit. Zero or negative concurrency is replaced with the default.
// Canceling ctx stops intake, not accepted dispatch. Call Drain with a deadline
// during shutdown to bound the remaining dispatch work.
func New(ctx context.Context, logger *slog.Logger, scheduler model.JobHandler, maxInFlight, concurrency, workQueueLimit int) *Limiter {
	if maxInFlight < 0 {
		// Using panic, because getting here is severe programmer error and the
		// whole controller is still just starting up.
		panic(fmt.Sprintf("maxInFlight < 0 (got %d)", maxInFlight))
	}
	maxInFlightGauge.Set(float64(maxInFlight))
	if concurrency <= 0 {
		concurrency = config.DefaultJobCreationConcurrency
	}
	if workQueueLimit <= 0 {
		workQueueLimit = config.DefaultWorkQueueLimit
	}
	dispatchCtx, cancelDispatch := context.WithCancel(context.WithoutCancel(ctx))
	l := &Limiter{
		handler:                scheduler,
		MaxInFlight:            maxInFlight,
		JobCreationConcurrency: concurrency,
		WorkQueueLimit:         workQueueLimit,
		logger:                 logger,
		newWork:                make(chan struct{}),
		jobs:                   make(map[string]*jobState),
		intakeCtx:              ctx,
		dispatchCtx:            dispatchCtx,
		cancelDispatch:         cancelDispatch,
		workersDone:            make(chan struct{}),
	}
	func() {
		metricCallbackMu.Lock()
		defer metricCallbackMu.Unlock()
		// Rather than calling gauge.Set, get the number of tokens during scrape.
		// Provide callbacks for tokens_available and work_queue_length gauges.
		tokensAvailableFunc = func() int {
			l.mu.Lock()
			defer l.mu.Unlock()
			return max(0, l.MaxInFlight-l.inFlight)
		}
		workQueueLengthFunc = func() int {
			l.mu.Lock()
			defer l.mu.Unlock()
			return len(l.queue)
		}
	}()

	l.workerWG.Add(concurrency)
	for range concurrency {
		go l.worker(ctx)
	}
	go func() {
		l.workerWG.Wait()
		cancelDispatch()
		close(l.workersDone)
	}()
	return l
}

// Pause pauses (or un-pauses) the queue.
func (l *Limiter) Pause(pause bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.paused = pause
	l.notifyWorkers()
}

// notifyWorkers wakes workers to recheck the queue and capacity. l.mu must be held.
func (l *Limiter) notifyWorkers() {
	// Capacity can return with an empty queue. All workers must wake so
	// their capacity-wait timers and gauges switch to waiting for work.
	close(l.newWork)
	l.newWork = make(chan struct{})
}

// RegisterInformer registers the limiter to listen for Kubernetes job events,
// and waits for cache sync. Call it before HandleMany so inherited jobs are
// counted before dispatch begins.
func (l *Limiter) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	reg, err := jobInformer.AddEventHandler(l)
	if err != nil {
		return err
	}
	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), reg.HasSynced) {
		return fmt.Errorf("failed to sync informer cache")
	}

	informerStore := jobInformer.GetStore()
	informerCacheKeys := informerStore.ListKeys()
	l.logger.Debug("informer cache sync complete, dump informer cache keys...",
		"cache-keys-found", len(informerCacheKeys),
	)

	for _, cacheKey := range informerCacheKeys {
		l.logger.Debug("informer cache key found",
			"informer-cache-key", fmt.Sprint(cacheKey),
		)
	}

	return nil
}

// HandleMany enqueues jobs. Workers dequeue jobs and schedule them.
func (l *Limiter) HandleMany(ctx context.Context, jobs []*api.AgentScheduledJob) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if l.stopping || l.intakeCtx.Err() != nil {
		return context.Canceled
	}
	l.queue = append(l.queue, jobs...)
	if len(l.queue) == 0 {
		return nil
	}

	// Deduplicate the queue by ID. The deduper component (next in the chain)
	// does this too but deals with jobs in the k8s cluster.
	l.queue = inPlaceDedup(l.queue)

	// Sort by priority, preserving existing ordering between jobs with equal
	// priority.
	slices.SortStableFunc(l.queue, func(a, b *api.AgentScheduledJob) int {
		// Higher number = higher priority.
		// See https://buildkite.com/docs/pipelines/configure/workflows/managing-priorities
		return cmp.Compare(b.Priority, a.Priority)
	})

	// Drop from the end of the queue to fit within the WorkQueueLimit.
	// Do this after sorting so the highest priority jobs are more likely to
	// remain.
	l.queue = l.queue[:min(len(l.queue), l.WorkQueueLimit)]

	l.notifyWorkers()
	return nil
}

func (l *Limiter) tryDequeueWork() (*api.AgentScheduledJob, *jobState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping || l.intakeCtx.Err() != nil || l.paused || (l.MaxInFlight > 0 && l.inFlight >= l.MaxInFlight) {
		return nil, nil
	}
	for len(l.queue) > 0 {
		job := l.queue[0]
		l.queue = l.queue[1:]
		if _, known := l.jobs[job.ID]; known {
			continue
		}
		state := &jobState{}
		l.jobs[job.ID] = state
		l.inFlight++
		l.notifyWorkers()
		return job, state
	}
	return nil, nil
}

func (l *Limiter) worker(ctx context.Context) {
	defer l.workerWG.Done()

	var capacityWaitStart time.Time
	for {
		start := time.Now()
		l.mu.Lock()
		if l.stopping {
			l.mu.Unlock()
			return
		}
		atCapacity := l.MaxInFlight > 0 && l.inFlight >= l.MaxInFlight
		changed := l.newWork
		ready := !l.paused && !atCapacity && len(l.queue) > 0
		l.mu.Unlock()
		if !atCapacity && !capacityWaitStart.IsZero() {
			tokenWaitDurationHistogram.Observe(time.Since(capacityWaitStart).Seconds())
			capacityWaitStart = time.Time{}
		}
		gauge := waitingForWorkGauge
		if atCapacity {
			gauge = waitingForTokenGauge
			if capacityWaitStart.IsZero() {
				capacityWaitStart = start
			}
		}
		if !ready {
			gauge.Inc()
			select {
			case <-ctx.Done():
				gauge.Dec()
				return

			case <-changed:
			}
			gauge.Dec()
			if !atCapacity {
				workWaitDurationHistogram.Observe(time.Since(start).Seconds())
			}
		}
		if ctx.Err() != nil {
			return
		}

		job, state := l.tryDequeueWork()
		if job == nil {
			continue
		}
		// Acquiring a slot also ends the wait, even if this worker fills the
		// last available slot before it can observe capacity at the loop top.
		if !capacityWaitStart.IsZero() {
			tokenWaitDurationHistogram.Observe(time.Since(capacityWaitStart).Seconds())
			capacityWaitStart = time.Time{}
		}

		l.logger.Debug("passing job to next handler",
			"handler", reflect.TypeOf(l.handler),
		)
		jobHandlerCallsCounter.Inc()
		if err := l.handler.Handle(l.dispatchCtx, job); err != nil {
			l.mu.Lock()
			// An observed job still occupies its slot even when scheduling
			// failed. An unobserved attempt can be released: a duplicate may
			// belong to a different stack outside this informer's scope.
			// Don't release a slot already released by a notification.
			if l.jobs[job.ID] == state && !state.observed {
				delete(l.jobs, job.ID)
				l.inFlight--
				l.notifyWorkers()
			}
			l.mu.Unlock()

			switch {
			case errors.Is(err, model.ErrDuplicateJob):
				jobHandlerErrorCounter.WithLabelValues("duplicate").Inc()
			case errors.Is(err, model.ErrStaleJob):
				jobHandlerErrorCounter.WithLabelValues("stale").Inc()
			default:
				jobHandlerErrorCounter.WithLabelValues("other").Inc()
			}

			l.logger.Debug("next handler failed",
				"job-uuid", job.ID,
				"error", err,
			)
			continue
		}
	}
}

// Drain stops intake and waits only for dispatches already admitted to a worker.
// Queued jobs have not been reserved and are left for another controller.
// If ctx expires, cancel dispatch and return even if a handler ignores cancellation.
func (l *Limiter) Drain(ctx context.Context) error {
	l.mu.Lock()
	l.stopping = true
	l.queue = nil
	l.notifyWorkers()
	l.mu.Unlock()

	select {
	case <-l.workersDone:
		return nil
	case <-ctx.Done():
		l.cancelDispatch()
		return ctx.Err()
	}
}

// Wait blocks until all worker goroutines return. Call Drain to bound this wait
// when shutting down with dispatches still in progress.
func (l *Limiter) Wait() { <-l.workersDone }

// OnAdd is called by k8s to inform us a resource is added.
func (l *Limiter) OnAdd(obj any, inInitialList bool) {
	onAddEventCounter.Inc()
	if job, ok := obj.(*batchv1.Job); ok {
		l.observeJob(job, false)
	}
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (l *Limiter) OnUpdate(prev, curr any) {
	onUpdateEventCounter.Inc()
	if job, ok := curr.(*batchv1.Job); ok {
		l.observeJob(job, false)
	}
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (l *Limiter) OnDelete(obj any) {
	onDeleteEventCounter.Inc()
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	if job, ok := obj.(*batchv1.Job); ok {
		l.observeJob(job, true)
	}
}

func (l *Limiter) observeJob(job *batchv1.Job, deleted bool) {
	id := job.Labels[config.UUIDLabel]
	if id == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	previousInFlight := l.inFlight
	state := l.jobs[id]
	if state == nil {
		if deleted {
			return
		}
		state = &jobState{finished: model.JobFinished(job)}
		l.jobs[id] = state
		if !state.finished {
			l.inFlight++
		}
	}
	state.observed = true
	if !state.finished && (deleted || model.JobFinished(job)) {
		state.finished = true
		l.inFlight--
	}
	if deleted {
		delete(l.jobs, id)
	}
	if l.inFlight != previousInFlight {
		l.notifyWorkers()
	}
	l.logger.Debug("job accounting updated",
		"job-uuid", id,
		"in-flight", l.inFlight,
		"deleted", deleted,
	)
}

func inPlaceDedup(jobs []*api.AgentScheduledJob) []*api.AgentScheduledJob {
	ids := make(map[string]struct{})
	i := 0 // here's where the next unique job will go
	for j, job := range jobs {
		if _, exists := ids[job.ID]; exists {
			continue // not unique
		}
		ids[job.ID] = struct{}{}
		if i != j {
			jobs[i] = job
		}
		i++
	}
	return jobs[:i]
}
