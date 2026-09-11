package jatissuer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/buildkite/roko"
)

type client interface {
	IssueJobAcquisitionTokens(context.Context, []string, int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error)
}

type Issuer struct {
	logger               *slog.Logger
	client               client
	handler              model.JobHandler
	tokenLifetimeSeconds int
}

func New(logger *slog.Logger, client client, handler model.JobHandler, tokenLifetimeSeconds int) *Issuer {
	return &Issuer{logger: logger, client: client, handler: handler, tokenLifetimeSeconds: tokenLifetimeSeconds}
}

func (i *Issuer) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	i.logger.Debug("issuing job acquisition token", "job-uuid", job.ID)
	retrier := roko.NewRetrier(
		roko.WithStrategy(roko.ExponentialSubsecond(time.Second)),
		roko.WithJitterRange(-time.Second, time.Second),
		roko.WithMaxAttempts(5),
	)
	response, err := roko.DoFunc(ctx, retrier, func(*roko.Retrier) (*api.IssueJobAcquisitionTokensResponse, error) {
		response, retryAfter, err := i.client.IssueJobAcquisitionTokens(ctx, []string{job.ID}, i.tokenLifetimeSeconds)
		if api.IsPermanentError(err) {
			retrier.Break()
		}
		retrier.SetNextInterval(max(retryAfter, retrier.NextInterval()))
		return response, err
	})
	if err != nil {
		i.logger.Warn("job acquisition token issuance failed", "job-uuid", job.ID, "error", err)
		return fmt.Errorf("issuing job acquisition token for job %s: %w", job.ID, err)
	}
	if response == nil || len(response.NotIssued) != 0 || len(response.JobAcquisitionTokens) != 1 {
		i.logger.Warn("job acquisition token was not issued", "job-uuid", job.ID)
		return fmt.Errorf("job acquisition token was not issued for job %s", job.ID)
	}
	issued := response.JobAcquisitionTokens[0]
	if issued.JobUUID != job.ID || issued.JobAcquisitionToken == "" {
		i.logger.Warn("invalid job acquisition token response", "job-uuid", job.ID)
		return fmt.Errorf("invalid job acquisition token response for job %s", job.ID)
	}
	job.JobAcquisitionToken = issued.JobAcquisitionToken
	return i.handler.Handle(ctx, job)
}
