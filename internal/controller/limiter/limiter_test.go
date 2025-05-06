package limiter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/google/uuid"
	"go.uber.org/zap/zaptest"
)

func TestLimiter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	fakeSched := model.NewFakeScheduler(1, nil)
	limiter := limiter.New(ctx, zaptest.NewLogger(t), fakeSched, 1, 1, -1)
	fakeSched.EventHandler = limiter
	fakeSched.Add(50)

	// simulate receiving a bunch of jobs
	var jobs []*api.AgentScheduledJob
	for range 50 {
		jobs = append(jobs, &api.AgentScheduledJob{ID: uuid.New().String()})
	}
	if err := limiter.HandleMany(ctx, jobs); err != nil {
		t.Errorf("limiter.HandleMany(ctx, jobs) = %v", err)
	}
	fakeSched.Wait()

	if got, want := len(fakeSched.Running), 0; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
	if got, want := len(fakeSched.Finished), 50; got != want {
		t.Errorf("len(fakeSched.Finished) = %d, want %d", got, want)
	}
	if got, want := fakeSched.Errors, 0; got != want {
		t.Errorf("fakeSched.Errors = %d, want %d", got, want)
	}

	// Wait for the limiter workers to exit (to avoid logging after the test)
	cancel()
	limiter.Wait()
}

func TestLimiter_SchedulerErrors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fakeSched := model.NewFakeScheduler(0, errors.New("invalid"))
	limiter := limiter.New(ctx, zaptest.NewLogger(t), fakeSched, 1, 1, -1)
	fakeSched.EventHandler = limiter
	fakeSched.Add(50)

	var jobs []*api.AgentScheduledJob
	for range 50 {
		jobs = append(jobs, &api.AgentScheduledJob{ID: uuid.New().String()})
	}
	if err := limiter.HandleMany(ctx, jobs); err != nil {
		t.Errorf("limiter.HandleMany(ctx, jobs) = %v", err)
	}
	fakeSched.Wait()

	if got, want := len(fakeSched.Running), 0; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
	if got, want := len(fakeSched.Finished), 0; got != want {
		t.Errorf("len(fakeSched.Finished) = %d, want %d", got, want)
	}
	if got, want := fakeSched.Errors, 50; got != want {
		t.Errorf("fakeSched.Errors = %d, want %d", got, want)
	}

	// Wait for the limiter workers to exit (to avoid logging after the test)
	cancel()
	limiter.Wait()
}
