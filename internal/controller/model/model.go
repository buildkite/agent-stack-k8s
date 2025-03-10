// Package model holds shared types and values.
package model

import (
	"context"
	"errors"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"

	batchv1 "k8s.io/api/batch/v1"
)

// ErrDuplicateJob is a sentinel error returned when a job has already been
// scheduled.
var ErrDuplicateJob = errors.New("job already scheduled")

// ErrStaleJob is a sentinel error returned when the job becomes too stale to
// begin scheduling.
var ErrStaleJob = errors.New("stale-job-data-timeout")

// JobHandler implementations can handle a job.
type JobHandler interface {
	Handle(context.Context, Job) error
}

// Job wraps the Buildkite command job with extra information.
type Job struct {
	// The job information.
	*api.CommandJob

	// Closed when the job information becomes stale.
	StaleCh <-chan struct{}

	// When we began the Buildkite GraphQL query that returned this job.
	QueriedAt time.Time
}

// JobFinished reports if the job has a Complete or Failed status condition.
func JobFinished(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		switch cond.Type {
		case batchv1.JobComplete, batchv1.JobFailed:
			// Per the API docs, these are the only terminal job conditions.
			return true
		}
	}
	return false
}
