package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/uuid"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// BuildkiteJobChecker monitors Buildkite jobs for cancellation state changes.
// It operate on individual jobs basis, relying on legacy Agent API.
// In the future version, we will get rid of it.
type BuildkiteJobChecker struct {
	logger      *zap.Logger
	agentClient *api.AgentClient
	k8s         kubernetes.Interface

	// The job cancel checkers query the job state every so often.
	jobCancelCheckerInterval time.Duration

	// Channels that are closed when a cancel checker should stop.
	cancelCheckerChsMu sync.Mutex
	cancelCheckerChs   map[uuid.UUID]*onceChan
}

// NewBuildkiteJobChecker creates a new Buildkite job checker.
func NewBuildkiteJobChecker(
	logger *zap.Logger,
	agentClient *api.AgentClient,
	k8s kubernetes.Interface,
	interval time.Duration,
) *BuildkiteJobChecker {
	return &BuildkiteJobChecker{
		logger:                   logger,
		agentClient:              agentClient,
		k8s:                      k8s,
		jobCancelCheckerInterval: interval,
		cancelCheckerChs:         make(map[uuid.UUID]*onceChan),
	}
}

// StartChecking starts monitoring a job for cancellation.
// This should only be called for jobs in pending state.
func (c *BuildkiteJobChecker) StartChecking(ctx context.Context, log *zap.Logger, podMeta metav1.ObjectMeta, jobUUID uuid.UUID) {
	c.cancelCheckerChsMu.Lock()
	defer c.cancelCheckerChsMu.Unlock()

	if c.cancelCheckerChs[jobUUID] != nil {
		// The checker is already running or has run.
		return
	}
	stopCh := make(chan struct{})
	c.cancelCheckerChs[jobUUID] = &onceChan{ch: stopCh}
	go c.jobCancelChecker(ctx, stopCh, log, podMeta, jobUUID)
}

// StopChecking stops monitoring a job for cancellation.
// This should be called when jobs leave pending state.
func (c *BuildkiteJobChecker) StopChecking(jobUUID uuid.UUID) {
	c.cancelCheckerChsMu.Lock()
	defer c.cancelCheckerChsMu.Unlock()
	if ch := c.cancelCheckerChs[jobUUID]; ch != nil {
		ch.closeOnce()
		delete(c.cancelCheckerChs, jobUUID)
	}
}

// GetActiveCheckCount returns the number of jobs currently being checked.
func (c *BuildkiteJobChecker) GetActiveCheckCount() int {
	c.cancelCheckerChsMu.Lock()
	defer c.cancelCheckerChsMu.Unlock()
	return len(c.cancelCheckerChs)
}

// jobCancelChecker runs a loop that queries Buildkite for the job state, and
// calls the callback if the job becomes cancelled. This should only be used for
// pods that are still pending: stopCh should be closed as soon as the agent
// container starts running.
func (c *BuildkiteJobChecker) jobCancelChecker(ctx context.Context, stopCh <-chan struct{}, log *zap.Logger, podMeta metav1.ObjectMeta, jobUUID uuid.UUID) {
	log.Debug("Checking job state for cancellation")
	defer log.Debug("Stopped checking job state for cancellation")

	ticker := time.NewTicker(c.jobCancelCheckerInterval)
	defer ticker.Stop()

	retryAfterCh := time.After(0)

	for {
		select {
		case <-ctx.Done():
			return

		case <-stopCh:
			return

		case <-ticker.C:
			// continue below
		}

		// Also wait for retryAfter, if set
		select {
		case <-ctx.Done():
			return

		case <-stopCh:
			return

		case <-retryAfterCh:
			// continue below
		}

		job, retryAfter, err := c.agentClient.GetJobState(ctx, jobUUID.String())
		if api.IsPermanentError(err) {
			log.Error("Couldn't fetch state of job", zap.Error(err))
			return
		}
		retryAfterCh = time.After(retryAfter)
		if err != nil {
			// *shrug* Check again soon.
			continue
		}
		log := log.With(zap.String("job_state", string(job.State)))

		switch job.State {
		case api.JobStateCanceled, api.JobStateCanceling:
			log.Info("Deleting pending pod for cancelled job")
			// Please read pod_watcher to understand that this logic only applies to Pending pods.
			if err := forcefullyDeletePod(ctx, log, c.k8s, &podMeta, "job_cancelled"); err != nil {
				// Low likelihood, we will retry.
				continue
			}
			return

		case api.JobStateScheduled, api.JobStateReserved:
			// The pod can continue waiting for resources / initializing.
			// Technically, when it's on reserved state we should check if the current stack is the owner.
			// But since we "reserver" in the beginning of our pipeline. We trust the current stack runtime be the owner
			// of the job in this context.

		default:
			// Assigned, Accepted, Running: Too late. Let the agent within
			// the pod handle cancellation. Finished, etc: it's already over.
			// If it's any other state, we probably shouldn't interfere.
			log.Debug("Ending job cancel checker due to job state")
			return
		}
	}
}

// onceChan stores a channel and a [sync.Once] to be used for closing the
// channel at most once.
type onceChan struct {
	once sync.Once
	ch   chan struct{}
}

func (oc *onceChan) closeOnce() {
	if oc == nil {
		return
	}
	oc.once.Do(func() { close(oc.ch) })
}
