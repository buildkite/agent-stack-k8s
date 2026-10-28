package reserver

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/buildkite/stacksapi"
)

type client interface {
	ReserveJobs(context.Context, []string, int) (*stacksapi.BatchReserveJobsResponse, time.Duration, error)
}

// Reserver reserves a job after the limiter has allocated capacity for it,
// then passes it to the next handler.
type Reserver struct {
	handler     model.JobHandler
	agentClient client
	ctx         context.Context
	requests    chan reservationRequest

	// Logs goes here
	logger *slog.Logger

	// reservationExpirySeconds controls how long a job reservation is held.
	// 0 means use the server default (900 seconds / 15 minutes).
	reservationExpirySeconds int
}

type reservationRequest struct {
	ctx    context.Context
	id     string
	result chan reservationResult
}

type reservationResult struct {
	reserved bool
	expires  time.Time
	err      error
}

func New(ctx context.Context, logger *slog.Logger, agentClient client, nextHandler model.JobHandler, reservationExpirySeconds int) *Reserver {
	r := &Reserver{
		handler:                  nextHandler,
		agentClient:              agentClient,
		ctx:                      ctx,
		requests:                 make(chan reservationRequest),
		logger:                   logger,
		reservationExpirySeconds: reservationExpirySeconds,
	}
	go r.run(ctx)
	return r
}

// Handle reserves a job and forwards it only if the reservation succeeds.
// A failed reservation must return an error so the limiter returns its slot.
func (r *Reserver) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	req := reservationRequest{ctx: ctx, id: job.ID, result: make(chan reservationResult, 1)}
	select {
	case r.requests <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
	select {
	case result := <-req.result:
		if err := ctx.Err(); err != nil {
			return err
		}
		if result.err != nil {
			r.logger.Warn("failed to reserve job", "job-uuid", job.ID, "error", result.err)
			return fmt.Errorf("reserving job %s: %w", job.ID, result.err)
		}
		if !result.reserved {
			return fmt.Errorf("job %s was not reserved; it may already be reserved or assigned to an agent", job.ID)
		}
		ctx, cancel := context.WithDeadline(ctx, result.expires)
		defer cancel()
		return r.handler.Handle(ctx, job)
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

func (r *Reserver) run(ctx context.Context) {
	for {
		var batch []reservationRequest
		select {
		case req := <-r.requests:
			batch = append(batch, req)
		case <-ctx.Done():
			return
		}
		// Only limiter-admitted workers can join this batch. Queued jobs must
		// remain unreserved even when the API could accept a larger batch.
		timer := time.NewTimer(10 * time.Millisecond)
	collect:
		for len(batch) < 1000 {
			select {
			case req := <-r.requests:
				batch = append(batch, req)
			case <-timer.C:
				break collect
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		r.reserve(ctx, batch)
	}
}

func (r *Reserver) reserve(ctx context.Context, batch []reservationRequest) {
	ids := make([]string, 0, len(batch))
	for _, req := range batch {
		if req.ctx.Err() == nil {
			ids = append(ids, req.id)
		}
	}
	if len(ids) == 0 {
		return
	}
	r.logger.Debug("reserving jobs via Stacks API", "count", len(ids))
	// Start before the request so latency cannot extend our local view of
	// the reservation beyond the server's expiry.
	lifetime := r.reservationExpirySeconds
	if lifetime == 0 {
		lifetime = 900
	}
	expires := time.Now().Add(time.Duration(lifetime) * time.Second)
	result, _, err := r.agentClient.ReserveJobs(ctx, ids, r.reservationExpirySeconds)
	for _, req := range batch {
		reserved := err == nil && slices.Contains(result.Reserved, req.id)
		req.result <- reservationResult{reserved: reserved, expires: expires, err: err}
	}
}
