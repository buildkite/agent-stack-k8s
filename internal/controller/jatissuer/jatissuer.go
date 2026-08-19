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
	IssueJobAcquisitionTokens(context.Context, []string) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error)
}

type Issuer struct {
	logger  *slog.Logger
	client  client
	handler model.JobHandler
}

func New(logger *slog.Logger, client client, handler model.JobHandler) *Issuer {
	return &Issuer{logger: logger, client: client, handler: handler}
}

func (i *Issuer) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	i.logger.Debug("issuing job acquisition token", "job-uuid", job.ID)
	retrier := roko.NewRetrier(
		roko.WithStrategy(roko.ExponentialSubsecond(time.Second)),
		roko.WithJitterRange(-time.Second, time.Second),
		roko.WithMaxAttempts(5),
	)
	response, err := roko.DoFunc(ctx, retrier, func(*roko.Retrier) (*api.IssueJobAcquisitionTokensResponse, error) {
		response, retryAfter, err := i.client.IssueJobAcquisitionTokens(ctx, []string{job.ID})
		if api.IsPermanentError(err) {
			retrier.Break()
		}
		retrier.SetNextInterval(max(retryAfter, retrier.NextInterval()))
		return response, err
	})
	if err != nil {
		return fmt.Errorf("issuing job acquisition token for job %s: %w", job.ID, err)
	}
	if response == nil || len(response.NotIssued) != 0 || len(response.JobAcquisitionTokens) != 1 {
		return fmt.Errorf("job acquisition token was not issued for job %s", job.ID)
	}
	issued := response.JobAcquisitionTokens[0]
	if issued.JobUUID != job.ID || issued.JobAcquisitionToken == "" {
		return fmt.Errorf("invalid job acquisition token response for job %s", job.ID)
	}
	job.JobAcquisitionToken = issued.JobAcquisitionToken
	return i.handler.Handle(ctx, job)
}
