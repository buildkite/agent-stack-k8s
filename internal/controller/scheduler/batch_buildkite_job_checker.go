package scheduler

import (
	"context"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/uuid"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// BatchBuildkiteJobChecker monitors Buildkite jobs for cancellation state changes.
// Unlike the old legacy BuildkiteJobChecker, this checker check all pending jobs together,
// relying on the new Stack API.
type BatchBuildkiteJobChecker struct {
	logger      *zap.Logger
	agentClient *api.AgentClient
	k8s         kubernetes.Interface

	// The job cancel checkers query the job state every so often.
	jobCancelCheckerInterval time.Duration

	// Store jobs that we will check against.
	checkingJobsMu sync.Mutex
	checkingJobs   map[uuid.UUID]metav1.ObjectMeta
}

// NewBatchBuildkiteJobChecker creates a new Batch Buildkite job checker.
func NewBatchBuildkiteJobChecker(
	logger *zap.Logger,
	agentClient *api.AgentClient,
	k8s kubernetes.Interface,
	interval time.Duration,
) *BatchBuildkiteJobChecker {
	return &BatchBuildkiteJobChecker{
		logger:                   logger,
		agentClient:              agentClient,
		k8s:                      k8s,
		jobCancelCheckerInterval: interval,
		checkingJobs:             make(map[uuid.UUID]metav1.ObjectMeta),
	}
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

	// Create slices for job UUIDs and corresponding pod metadata
	jobUUIDs := make([]string, 0, len(c.checkingJobs))
	jobToPodMeta := make(map[string]metav1.ObjectMeta, len(c.checkingJobs))

	for jobUUID, podMeta := range c.checkingJobs {
		jobUUIDStr := jobUUID.String()
		jobUUIDs = append(jobUUIDs, jobUUIDStr)
		jobToPodMeta[jobUUIDStr] = podMeta
	}
	c.checkingJobsMu.Unlock() // Release the lock as soon as possible.

	// Split jobs into batches of 1000 and process concurrently
	const batchSize = 1000
	var wg sync.WaitGroup

	batchCount := int(math.Ceil(float64(len(jobUUIDs)) / float64(batchSize)))
	jobStatesCh := make(chan map[string]api.JobState, batchCount)

	for batch := range slices.Chunk(jobUUIDs, batchSize) {
		wg.Add(1)
		go func() {
			defer wg.Done()

			jobStates, _, err := c.agentClient.GetJobStates(ctx, batch)
			if err != nil {
				c.logger.Error("Couldn't fetch states of jobs", zap.Error(err), zap.Int("batch_size", len(batch)))
				return
			}

			jobStatesCh <- jobStates
		}()
	}

	// Wait for all batches to complete and close channel
	go func() {
		wg.Wait()
		close(jobStatesCh)
	}()

	// Process results from all batches
	for jobStates := range jobStatesCh {
		for jobUUIDStr, jobState := range jobStates {
			podMeta := jobToPodMeta[jobUUIDStr]
			// Concurrently handle cancelled jobs.
			// This is at the mercy of k8s API rate limit and buildkite stack API rate limit.
			// If either rate limit were breached, it will result in delay in resource release.
			go c.handleJobState(ctx, jobUUIDStr, jobState, podMeta)
		}
	}
}

func (c *BatchBuildkiteJobChecker) AddJob(jobUUID uuid.UUID, podMeta metav1.ObjectMeta) {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	c.checkingJobs[jobUUID] = podMeta
}

func (c *BatchBuildkiteJobChecker) StopCheckingJob(jobUUID uuid.UUID) {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	delete(c.checkingJobs, jobUUID)
}

func (c *BatchBuildkiteJobChecker) handleJobState(ctx context.Context, jobUUIDStr string, jobState api.JobState, podMeta metav1.ObjectMeta) {
	log := c.logger.With(zap.String("job_uuid", jobUUIDStr), zap.String("job_state", string(jobState)))

	switch jobState {
	case api.JobStateCanceled, api.JobStateCanceling:
		log.Info("Deleting pending pod for cancelled job")
		if err := forcefullyDeletePod(ctx, log, c.k8s, &podMeta, "job_cancelled"); err != nil {
			log.Error("Failed to delete pod for cancelled job", zap.Error(err))
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

// GetActiveCheckCount returns the number of jobs currently being checked.
func (c *BatchBuildkiteJobChecker) GetActiveCheckCount() int {
	c.checkingJobsMu.Lock()
	defer c.checkingJobsMu.Unlock()
	return len(c.checkingJobs)
}
