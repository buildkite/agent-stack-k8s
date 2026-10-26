package reserver

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
)

// Reserver reserves a job after the limiter has allocated capacity for it,
// then passes it to the next handler.
type Reserver struct {
	handler     model.JobHandler
	agentClient *api.AgentClient

	// Logs goes here
	logger *slog.Logger

	// reservationExpirySeconds controls how long a job reservation is held.
	// 0 means use the server default (900 seconds / 15 minutes).
	reservationExpirySeconds int
}

func New(logger *slog.Logger, agentClient *api.AgentClient, nextHandler model.JobHandler, reservationExpirySeconds int) *Reserver {
	r := &Reserver{
		handler:                  nextHandler,
		agentClient:              agentClient,
		logger:                   logger,
		reservationExpirySeconds: reservationExpirySeconds,
	}

	return r
}

// Handle reserves a job and forwards it only if the reservation succeeds.
// A failed reservation must return an error so the limiter returns its slot.
func (r *Reserver) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	r.logger.Debug("reserving job via Stacks API", "job-uuid", job.ID)
	result, _, err := r.agentClient.ReserveJobs(ctx, []string{job.ID}, r.reservationExpirySeconds)
	if err != nil {
		return fmt.Errorf("reserving job %s: %w", job.ID, err)
	}
	if !slices.Contains(result.Reserved, job.ID) {
		return fmt.Errorf("job %s was not reserved; it may already be reserved or assigned to an agent", job.ID)
	}
	return r.handler.Handle(ctx, job)
}
