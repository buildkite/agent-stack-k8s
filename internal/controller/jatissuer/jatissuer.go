package jatissuer

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/buildkite/roko"
	"golang.org/x/time/rate"
)

type client interface {
	IssueJobAcquisitionTokens(context.Context, []string, int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error)
}

type Issuer struct {
	logger               *slog.Logger
	client               client
	handler              model.JobHandler
	tokenLifetimeSeconds int
	requests             chan tokenRequest
	rate                 *rate.Limiter
	ctx                  context.Context
}

type tokenRequest struct {
	ctx    context.Context
	id     string
	result chan tokenResult
}

type tokenResult struct {
	token api.JobAcquisitionToken
	err   error
}

func New(ctx context.Context, logger *slog.Logger, client client, handler model.JobHandler, tokenLifetimeSeconds int) *Issuer {
	i := &Issuer{
		logger: logger, client: client, handler: handler, tokenLifetimeSeconds: tokenLifetimeSeconds,
		requests: make(chan tokenRequest), ctx: ctx,
		// One issuer per stack, with no initial burst. Every attempt, including
		// retries, goes through the same 10 requests/second allowance.
		rate: rate.NewLimiter(rate.Every(100*time.Millisecond), 1),
	}
	go i.run(ctx)
	return i
}

func (i *Issuer) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	r := tokenRequest{ctx: ctx, id: job.ID, result: make(chan tokenResult, 1)}
	select {
	case i.requests <- r:
	case <-ctx.Done():
		return ctx.Err()
	case <-i.ctx.Done():
		return i.ctx.Err()
	}
	select {
	case result := <-r.result:
		if err := ctx.Err(); err != nil {
			return err
		}
		if result.err != nil {
			return result.err
		}
		job.JobAcquisitionToken = result.token
		return i.handler.Handle(ctx, job)
	case <-ctx.Done():
		return ctx.Err()
	case <-i.ctx.Done():
		return i.ctx.Err()
	}
}

func (i *Issuer) run(ctx context.Context) {
	for {
		var batch []tokenRequest
		select {
		case r := <-i.requests:
			batch = append(batch, r)
		case <-ctx.Done():
			return
		}
		// Coalesce jobs only after they have capacity and a reservation, so
		// token lifetime isn't spent waiting in the limiter's priority queue.
		timer := time.NewTimer(10 * time.Millisecond)
	collect:
		for len(batch) < 1000 {
			select {
			case r := <-i.requests:
				batch = append(batch, r)
			case <-timer.C:
				break collect
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		i.issue(ctx, batch)
	}
}

func (i *Issuer) issue(ctx context.Context, batch []tokenRequest) {
	// Stop even a long Retry-After as soon as every caller has canceled or
	// reached its reservation deadline, so later batches can proceed.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var remaining atomic.Int32
	remaining.Store(int32(len(batch)))
	for _, r := range batch {
		stop := context.AfterFunc(r.ctx, func() {
			if remaining.Add(-1) == 0 {
				cancel()
			}
		})
		defer stop()
	}
	strategy, name := roko.ExponentialSubsecond(time.Second)
	retrier := roko.NewRetrier(
		roko.WithStrategy(func(r *roko.Retrier) time.Duration {
			// Bound the backoff without overflowing an exponential strategy
			// when a reservation survives many transient failures.
			if r.AttemptCount() >= 8 {
				return 30*time.Second + r.Jitter()
			}
			return strategy(r)
		}, name),
		roko.WithJitterRange(-time.Second, time.Second),
		roko.TryForever(),
	)
	response, err := roko.DoFunc(ctx, retrier, func(*roko.Retrier) (*api.IssueJobAcquisitionTokensResponse, error) {
		if err := i.rate.Wait(ctx); err != nil {
			retrier.Break()
			return nil, err
		}
		ids := make([]string, 0, len(batch))
		active := batch[:0]
		for _, r := range batch {
			if r.ctx.Err() == nil {
				active = append(active, r)
				ids = append(ids, r.id)
			}
		}
		batch = active
		if len(batch) == 0 {
			return nil, nil
		}
		i.logger.Debug("issuing job acquisition tokens", "count", len(ids))
		response, retryAfter, err := i.client.IssueJobAcquisitionTokens(ctx, ids, i.tokenLifetimeSeconds)
		if api.IsPermanentError(err) {
			retrier.Break()
		}
		retrier.SetNextInterval(max(retryAfter, retrier.NextInterval()))
		if err != nil {
			i.logger.Warn("failed to issue job acquisition tokens", "count", len(ids), "error", err)
		}
		return response, err
	})
	// Correlate by UUID, never response order. A partial response must not
	// prevent other jobs in the batch from being dispatched.
	tokens := make(map[string]api.JobAcquisitionToken)
	if response != nil {
		for _, issued := range response.JobAcquisitionTokens {
			if _, duplicate := tokens[issued.JobUUID]; duplicate {
				tokens[issued.JobUUID] = ""
			} else {
				tokens[issued.JobUUID] = issued.JobAcquisitionToken
			}
		}
		for _, id := range response.NotIssued {
			tokens[id] = ""
		}
	}
	for _, r := range batch {
		result := tokenResult{token: tokens[r.id], err: err}
		if err == nil && result.token == "" {
			result.err = fmt.Errorf("job acquisition token was not issued for job %s", r.id)
		}
		r.result <- result
	}
}
