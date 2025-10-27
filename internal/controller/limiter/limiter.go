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
	// in the cluster. Must be at least 1.
	MaxInFlight int

	// Controls the number of workers that pass along jobs.
	JobCreationConcurrency int

	// Maximum number of jobs to store in the work queue.
	WorkQueueLimit int

	// Next handler in the chain.
	handler model.JobHandler

	// Logs go here
	logger *slog.Logger

	// tokenBucket implements the max-in-flight limit using a channel.
	// When a job starts, it takes a token from the bucket.
	// When a job ends, it puts a token back in the bucket.
	tokenBucket chan struct{}

	// A channel to notify workers that there is new work.
	newWork chan struct{}

	// A priority queue of pending jobs, and pause flag.
	queueMu sync.Mutex
	queue   []*api.AgentScheduledJob
	paused  bool

	// A wait group for workers (mainly for the benefit of unit tests).
	workerWG sync.WaitGroup
}

// New creates a Limiter. maxInFlight must be non-negative, but 0 is interpreted
// as no limit. Zero or negative concurrency is replaced with the default.
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
	l := &Limiter{
		handler:                scheduler,
		MaxInFlight:            maxInFlight,
		JobCreationConcurrency: concurrency,
		WorkQueueLimit:         workQueueLimit,
		logger:                 logger,
		tokenBucket:            make(chan struct{}, maxInFlight),
		newWork:                make(chan struct{}, concurrency),
	}
	if maxInFlight == 0 {
		// Reads on a closed channel always succeed immediately.
		close(l.tokenBucket)
	} else {
		for range maxInFlight {
			// Fill the bucket with tokens.
			l.tokenBucket <- struct{}{}
		}
	}
	// Rather than calling gauge.Set, get the number of tokens during scrape.
	// Provide a callback for tokensAvailableGauge.
	tokensAvailableFunc = func() int { return len(l.tokenBucket) }
	workQueueLengthFunc = func() int {
		l.queueMu.Lock()
		defer l.queueMu.Unlock()
		return len(l.queue)
	}

	l.workerWG.Add(concurrency)
	for range concurrency {
		go l.worker(ctx)
	}
	return l
}

// Pause pauses (or un-pauses) the queue.
func (l *Limiter) Pause(pause bool) {
	l.queueMu.Lock()
	defer l.queueMu.Unlock()

	l.paused = pause
	if pause {
		return
	}

	// Not paused. Let the workers know that work is available.
	for range min(l.JobCreationConcurrency, len(l.queue)) {
		select {
		case l.newWork <- struct{}{}:
		default:
		}
	}
}

// RegisterInformer registers the limiter to listen for Kubernetes job events,
// and waits for cache sync.
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
	l.queueMu.Lock()
	defer l.queueMu.Unlock()

	l.queue = append(l.queue, jobs...)
	if len(l.queue) == 0 {
		return nil
	}

	// Deduplicate the queue by ID. The deduper component (next in the chain)
	// does this too but deals with jobs in the k8s cluster.
	l.queue = inPlaceDedup(l.queue)

	// Sort by priority, preserving existing ordering between jobs with equal
	// priority.
	slices.SortStableFunc(jobs, func(a, b *api.AgentScheduledJob) int {
		// Higher number = higher priority.
		// See https://buildkite.com/docs/pipelines/configure/workflows/managing-priorities
		return cmp.Compare(b.Priority, a.Priority)
	})

	// Drop from the end of the queue to fit within the WorkQueueLimit.
	// Do this after sorting so the highest priority jobs are more likely to
	// remain.
	l.queue = l.queue[:min(len(l.queue), l.WorkQueueLimit)]

	if l.paused {
		return nil
	}

	// Tell the workers that work exists.
	for range min(l.JobCreationConcurrency, len(l.queue)) {
		select {
		case l.newWork <- struct{}{}:
		default:
		}
	}
	return nil
}

func (l *Limiter) tryDequeueWork() (job *api.AgentScheduledJob) {
	l.queueMu.Lock()
	defer l.queueMu.Unlock()
	if len(l.queue) == 0 || l.paused {
		return nil
	}
	job, l.queue = l.queue[0], l.queue[1:]
	if len(l.queue) > 0 {
		// More work exists.
		select {
		case l.newWork <- struct{}{}:
		default:
		}
	}
	return job
}

func (l *Limiter) worker(ctx context.Context) {
	defer l.workerWG.Done()

	for {
		// Track time to acquire a token.
		start := time.Now()

		l.logger.Debug("Waiting for token",
			"max-in-flight", l.MaxInFlight,
			"available-tokens", len(l.tokenBucket),
			"new-work", len(l.newWork),
		)

		// Block until there's a token in the bucket.
		waitingForTokenGauge.Inc()
		select {
		case <-ctx.Done():
			return

		case <-l.tokenBucket:
			// Continue below.
		}
		waitingForTokenGauge.Dec()
		tokenWaitDurationHistogram.Observe(time.Since(start).Seconds())
		l.logger.Debug("token acquired",
			"available-tokens", len(l.tokenBucket),
		)

		// We got a token from the bucket above! Is there work we can do?
		// Track time to acquire work.
		start = time.Now()
		l.logger.Debug("Waiting for work",
			"max-in-flight", l.MaxInFlight,
			"available-tokens", len(l.tokenBucket),
			"new-work", len(l.newWork),
		)
		waitingForWorkGauge.Inc()
		select {
		case <-ctx.Done():
			l.tryReturnToken("Handle")
			return

		case <-l.newWork:
			// Continue below.
		}
		waitingForWorkGauge.Dec()
		workWaitDurationHistogram.Observe(time.Since(start).Seconds())

		l.logger.Debug("Dequeueing some work",
			"max-in-flight", l.MaxInFlight,
			"available-tokens", len(l.tokenBucket),
			"new-work", len(l.newWork),
		)
		job := l.tryDequeueWork()
		if job == nil {
			// Got a token and even got notified of some work, but got no job
			// (maybe another worker got it, maybe the queue is paused).
			l.tryReturnToken("Handle")
			continue
		}

		l.logger.Debug("passing job to next handler",
			"handler", reflect.TypeOf(l.handler),
		)
		jobHandlerCallsCounter.Inc()
		if err := l.handler.Handle(ctx, job); err != nil {
			// Oh well. Return the token.
			l.tryReturnToken("Handle")

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
				"available-tokens", len(l.tokenBucket),
				"new-work", len(l.newWork),
				"error", err,
			)
			continue
		}
	}
}

// Wait blocks until all worker goroutines return.
func (l *Limiter) Wait() { l.workerWG.Wait() }

// OnAdd is called by k8s to inform us a resource is added.
func (l *Limiter) OnAdd(obj any, inInitialList bool) {
	onAddEventCounter.Inc()
	job, _ := obj.(*batchv1.Job)
	if job == nil {
		return
	}
	if !inInitialList {
		// After sync is finished, the limiter handler takes tokens directly.
		return
	}

	// Before sync is finished: we're learning about existing jobs, so we should
	// (try to) take tokens for unfinished jobs started by a previous controller.
	// If it's added as already finished, no need to take a token for it.
	// Otherwise, try to take one, but don't block (in case the stack was
	// restarted with a different limit).
	if !model.JobFinished(job) {
		l.tryTakeToken("OnAdd")
		l.logger.Debug("existing not-finished job discovered",
			"job-uuid", job.Labels[config.UUIDLabel],
			"tokens-available", len(l.tokenBucket),
		)
	}
}

// OnUpdate is called by k8s to inform us a resource is updated.
func (l *Limiter) OnUpdate(prev, curr any) {
	onUpdateEventCounter.Inc()
	prevState, _ := prev.(*batchv1.Job)
	currState, _ := curr.(*batchv1.Job)
	if prevState == nil || currState == nil {
		return
	}
	// Only take or return a token if the job state has *changed*.
	// The only valid change is from not-finished to finished.
	if !model.JobFinished(prevState) && model.JobFinished(currState) {
		l.tryReturnToken("OnUpdate")
		l.logger.Debug("job state changed from not-finished to finished",
			"job-uuid", currState.Labels[config.UUIDLabel],
			"tokens-available", len(l.tokenBucket),
		)
	}
}

// OnDelete is called by k8s to inform us a resource is deleted.
func (l *Limiter) OnDelete(obj any) {
	onDeleteEventCounter.Inc()
	prevState, _ := obj.(*batchv1.Job)
	if prevState == nil {
		return
	}

	// OnDelete gives us the last-known state prior to deletion.
	// If that state was finished, we've already returned a token.
	// If that state was not-finished, we need to return a token now.
	if !model.JobFinished(prevState) {
		l.tryReturnToken("OnDelete")
		l.logger.Debug("not-finished job was deleted",
			"job-uuid", prevState.Labels[config.UUIDLabel],
			"tokens-available", len(l.tokenBucket),
		)
	}
}

// tryTakeToken takes a token from the bucket, if one is available. It does not
// block.
func (l *Limiter) tryTakeToken(source string) {
	select {
	case <-l.tokenBucket:
		// Success.
		l.logger.Debug("Token taken successfully",
			"source", source,
			"remaining-tokens", len(l.tokenBucket),
		)
	default:
		l.logger.Debug("Failed to take token - bucket empty",
			"source", source,
			"max-in-flight", l.MaxInFlight,
		)
		tokenUnderflowCounter.WithLabelValues(source).Inc()
	}
}

// tryReturnToken returns a token to the bucket, if not full. It does not block.
func (l *Limiter) tryReturnToken(source string) {
	if l.MaxInFlight == 0 {
		return
	}
	select {
	case l.tokenBucket <- struct{}{}:
		// Success.
		l.logger.Debug("Token returned successfully",
			"source", source,
			"available-tokens", len(l.tokenBucket),
		)
	default:
		l.logger.Warn("Failed to return token - bucket full",
			"source", source,
			"max-in-flight", l.MaxInFlight,
		)
		tokenOverflowCounter.WithLabelValues(source).Inc()
	}
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
