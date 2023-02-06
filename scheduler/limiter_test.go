package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLimiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan monitor.Job, 10)
	output := make(chan monitor.Job, 10)
	limiter := NewLimiter(zaptest.NewLogger(t), input, output, 1)

	go limiter.Run(ctx)

	// simulate receiving a bunch of jobs
	for i := 0; i < 5; i++ {
		input <- monitor.Job{
			CommandJob: api.CommandJob{
				Uuid: fmt.Sprintf("job-%d", i),
			},
		}
	}

	for i := 0; i < 5; i++ {
		// read off the output job
		job := <-output

		// assert output is now empty
		require.Empty(t, output)
		// one is now in flight
		limiter.mu.RLock()
		require.Len(t, limiter.inFlight, 1)
		limiter.mu.RUnlock()

		// mark this job as completed
		limiter.OnUpdate(nil, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					api.UUIDLabel: job.Uuid,
				},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobComplete},
				},
			},
		})
		// none are in now flight
		limiter.mu.RLock()
		require.Empty(t, limiter.inFlight)
		limiter.mu.RUnlock()
	}
}

func TestSkipsDuplicateJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan monitor.Job)
	output := make(chan monitor.Job, 10)
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	limiter := NewLimiter(logger, input, output, 2)

	go limiter.Run(ctx)

	job := monitor.Job{
		CommandJob: api.CommandJob{
			Uuid: "always the same",
		},
	}
	input <- job

	// read the first job off
	<-output
	// put the same job on again
	input <- job

	// it gets dropped
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 1, logs.Len())
	require.Empty(t, output)

	// mark it as complete
	limiter.OnUpdate(nil, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				api.UUIDLabel: job.Uuid,
			},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete},
			},
		},
	})

	// put the same job on again
	input <- job
	// now it is output again
	<-output
}

func TestUnlimitedMaxInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan monitor.Job, 10)
	output := make(chan monitor.Job, 10)
	limiter := NewLimiter(zaptest.NewLogger(t), input, output, 0)

	go limiter.Run(ctx)

	// simulate receiving a bunch of jobs
	for i := 0; i < 5; i++ {
		input <- monitor.Job{
			CommandJob: api.CommandJob{
				Uuid: fmt.Sprintf("job-%d", i),
			},
		}
	}

	// output instantly gets all the jobs, no completions required
	var jobs []monitor.Job
	for i := 0; i < 5; i++ {
		jobs = append(jobs, <-output)
	}
	require.Len(t, jobs, 5)
	// assert that we're not just piling up completions we'll never look at
	require.Empty(t, limiter.completions)
}

func timePtr(t metav1.Time) *metav1.Time {
	return &t
}
