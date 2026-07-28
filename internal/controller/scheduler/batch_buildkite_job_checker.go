package scheduler

import (
	"context"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"log/slog"
)

// cancelTargetKind says what the checker should delete once it learns a
// Buildkite job was cancelled.
type cancelTargetKind int

const (
	// cancelTargetPod deletes the pod. Scheduler sets BackoffLimit=0 on the
	// k8s Job, so removing its pod also terminates the Job.
	cancelTargetPod cancelTargetKind = iota

	// cancelTargetJob deletes the k8s Job itself. Needed when no pod exists to
	// delete, which is the steady state for a Job held suspended by an
	// admission controller such as Kueue: registration used to be driven purely
	// by pod events, so those Jobs were never checked at all and only ran once
	// something else admitted them.
	cancelTargetJob
)

// cancelTarget is the object the checker deletes for one Buildkite job.
type cancelTarget struct {
	kind cancelTargetKind
	meta metav1.ObjectMeta
}

// BatchBuildkiteJobChecker monitors Buildkite jobs for cancellation state changes.
// Unlike the old legacy BuildkiteJobChecker, this checker check all pending jobs together,
// relying on the new Stack API.
type BatchBuildkiteJobChecker struct {
	logger      *slog.Logger
	agentClient *api.AgentClient
	k8s         kubernetes.Interface

	// The job cancel checkers query the job state every so often.
	jobCancelCheckerInterval time.Duration

	// Store jobs that we will check against, and what to delete for each.
	checkingJobsMu sync.Mutex
	checkingJobs   map[uuid.UUID]cancelTarget
}

// NewBatchBuildkiteJobChecker creates a new Batch Buildkite job checker.
func NewBatchBuildkiteJobChecker(
	logger *slog.Logger,
	agentClient *api.AgentClient,
	k8s kubernetes.Interface,
	interval time.Duration,
) *BatchBuildkiteJobChecker {
	c := &BatchBuildkiteJobChecker{
		logger:                   logger,
		agentClient:              agentClient,
		k8s:                      k8s,
		jobCancelCheckerInterval: interval,
		checkingJobs:             make(map[uuid.UUID]cancelTarget),
	}
	jobCancelCheckerGaugeFunc = c.GetActiveCheckCount
	return c
}

// StartChecking starts a gorouting loop to periodically check job states for all jobs that it manages.
func (c *BatchBuildkiteJobChecker) StartChecking(ctx context.Context) {
	go c.batchChecker(ctx)
}

func (c *BatchBuildkiteJobChecker) batchChecker(ctx context.Context) {
	c.logger.Debug("Starting batch job state checker")
	defer c.logger.Debug("Stopped batch job state checker")

	ticker := time.NewTicker(c.jobCancelCheckerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkJobStates(ctx)
		}
	}
}

func (c *BatchBuildkiteJobChecker) checkJobStates(ctx context.Context) {
	c.checkingJobsMu.Lock()
	if len(c.checkingJobs) == 0 {
		c.checkingJobsMu.Unlock()
		return
	}

	// Create slices for job UUIDs and corresponding deletion targets
	jobUUIDs := make([]string, 0, len(c.checkingJobs))
	jobToTarget := make(map[string]cancelTarget, len(c.checkingJobs))

	for jobUUID, target := range c.checkingJobs {
		jobUUIDStr := jobUUID.String()
		jobUUIDs = append(jobUUIDs, jobUUIDStr)
		jobToTarget[jobUUIDStr] = target
	}
	c.checkingJobsMu.Unlock() // Release the lock as soon as possible.

	// Split jobs into batches of 1000 and process concurrently
	const batchSize = 1000
	var wg sync.WaitGroup

	batchCount := int(math.Ceil(float64(len(jobUUIDs)) / float64(batchSize)))
	jobStatesCh := make(chan map[string]api.JobState, batchCount)

	for batch := range slices.Chunk(jobUUIDs, batchSize) {
		wg.Go(func() {

			jobStates, _, err := c.agentClient.GetJobStates(ctx, batch)
			if err != nil {
				c.logger.Error("Couldn't fetch states of jobs", "error", err, "batch_size", len(batch))
				return
			}

			jobStatesCh <- jobStates
		})
	}

	// Wait for all batches to complete and close channel
	go func() {
		wg.Wait()
		close(jobStatesCh)
	}()

	// Process results from all batches
	for jobStates := range jobStatesCh {
		for jobUUIDStr, jobState := range jobStates {
			target := jobToTarget[jobUUIDStr]
			// Concurrently handle cancelled jobs.
			// This is at the mercy of k8s API rate limit and buildkite stack API rate limit.
			// If either rate limit were breached, it will result in delay in resource release.
			go c.handleJobState(ctx, jobUUIDStr, jobState, target)
		}
	}
}

// AddPod registers a Buildkite job whose pod exists and is pending. A pod is
// the preferred deletion target, so this always replaces any Job registration.
func (c *BatchBuildkiteJobChecker) AddPod(jobUUID uuid.UUID, podMeta metav1.ObjectMeta) {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	c.checkingJobs[jobUUID] = cancelTarget{kind: cancelTargetPod, meta: podMeta}
}

// AddK8sJob registers a Buildkite job whose k8s Job exists but has no pod yet.
//
// This never downgrades an existing pod registration: pod events and Job events
// arrive independently, so a stale Job event must not replace a pod target that
// a later pod event installed.
func (c *BatchBuildkiteJobChecker) AddK8sJob(jobUUID uuid.UUID, jobMeta metav1.ObjectMeta) {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	if existing, ok := c.checkingJobs[jobUUID]; ok && existing.kind == cancelTargetPod {
		return
	}
	c.checkingJobs[jobUUID] = cancelTarget{kind: cancelTargetJob, meta: jobMeta}
}

func (c *BatchBuildkiteJobChecker) StopCheckingJob(jobUUID uuid.UUID) {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	delete(c.checkingJobs, jobUUID)
}

func (c *BatchBuildkiteJobChecker) handleJobState(ctx context.Context, jobUUIDStr string, jobState api.JobState, target cancelTarget) {
	log := c.logger.With("job_uuid", jobUUIDStr, "job_state", string(jobState))

	switch jobState {
	case api.JobStateCanceled, api.JobStateCanceling:
		var err error
		switch target.kind {
		case cancelTargetPod:
			log.Info("Deleting pending pod for cancelled job")
			err = forcefullyDeletePod(ctx, log, c.k8s, &target.meta, "job_cancelled")
		case cancelTargetJob:
			log.Info("Deleting podless k8s job for cancelled job")
			err = forcefullyDeleteJob(ctx, log, c.k8s, &target.meta, "job_cancelled")
		}
		if err != nil {
			log.Error("Failed to delete resource for cancelled job", "error", err)
			return
		}
		// Remove the job from checking list after successful deletion
		jobUUID, _ := uuid.Parse(jobUUIDStr)
		c.StopCheckingJob(jobUUID)

	case api.JobStateScheduled, api.JobStateReserved:
		// The pod can continue waiting for resources / initializing.

	default:
		// Assigned, Accepted, Running: Too late. Let the agent within
		// the pod handle cancellation. Finished, etc: it's already over.
		// If it's any other state, we probably shouldn't interfere.
		log.Debug("Ending job cancel checker due to job state")
		jobUUID, _ := uuid.Parse(jobUUIDStr)
		c.StopCheckingJob(jobUUID)
	}
}

// targetFor returns the registered deletion target for a Buildkite job.
func (c *BatchBuildkiteJobChecker) targetFor(jobUUID uuid.UUID) (cancelTarget, bool) {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	target, ok := c.checkingJobs[jobUUID]
	return target, ok
}

// GetActiveCheckCount returns the number of jobs currently being checked.
func (c *BatchBuildkiteJobChecker) GetActiveCheckCount() int {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	return len(c.checkingJobs)
}
