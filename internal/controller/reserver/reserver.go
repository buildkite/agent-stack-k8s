package reserver

import (
	"context"
	"fmt"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"go.uber.org/zap"
)

// Reserver is a JobHandler.
// It mark those bk jobs as reserved and then pass them to the next handler.
//
// In the future, we may:
// - use reserver as an actor to centralize posting reservation updates.
// - use reserver as a periodic worker to keep reserveration expiration up-to-date.
type Reserver struct {
	handler     model.ManyJobHandler
	agentClient *api.AgentClient

	// Logs goes here
	logger *zap.Logger

	enabled bool
	paused  bool
}

func New(logger *zap.Logger, agentClient *api.AgentClient, nextHandler model.ManyJobHandler, enabled bool) *Reserver {
	r := &Reserver{
		handler:     nextHandler,
		agentClient: agentClient,
		logger:      logger,
		enabled:     enabled,
	}

	return r
}

// Pause pauses (or un-pauses) the queue.
func (r *Reserver) Pause(pause bool) {
	r.handler.Pause(pause)
	r.paused = pause
}

// Rerserve a bunch of jobs and relay the successful reserved jobs to the next handler.
func (r *Reserver) HandleMany(ctx context.Context, jobs []*api.AgentScheduledJob) error {
	if r.paused {
		return nil
	}

	if !r.enabled {
		return r.handler.HandleMany(ctx, jobs)
	}

	var jobIDs []string
	for _, job := range jobs {
		jobIDs = append(jobIDs, job.ID)
	}

	r.logger.Info("reserving jobs via Agent API...", zap.Int("count", len(jobIDs)))

	result, _, err := r.agentClient.ReserveJobs(ctx, jobIDs, 15*time.Minute)
	if err != nil {
		return fmt.Errorf("error when reserving jobs: %w", err)
	}

	// There is a chance that this job is already reserved or assigned.
	// In general, we ignore the job and the job should execute correctly.
	// The worst case is that when controller crashes after job reservation and before k8s job were created.
	// In that case, a reservatation expiration is bound to happen.
	reservedJobs := findJobsIn(jobs, result.ReservedJobUUIDs)

	if len(reservedJobs) > 0 {
		return r.handler.HandleMany(ctx, reservedJobs)
	}
	return nil
}

func findJobsIn(jobs []*api.AgentScheduledJob, jobUUIDs []string) []*api.AgentScheduledJob {
	result := []*api.AgentScheduledJob{}

	// We expecting a max 1000 jobs input here, a O(N^2) solution here is likely fine.
	for _, job := range jobs {
		for _, reservedID := range jobUUIDs {
			if reservedID == job.ID {
				result = append(result, job)
				break
			}
		}
	}

	return result
}
