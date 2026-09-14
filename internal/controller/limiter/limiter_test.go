package limiter_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handleFunc func(context.Context, *api.AgentScheduledJob) error

func (f handleFunc) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	return f(ctx, job)
}

func TestLimiter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	fakeSched := model.NewFakeScheduler(1, nil)
	limiter := limiter.New(ctx, slog.Default(), fakeSched, 1, 1, -1)
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

	ctx, cancel := context.WithCancel(t.Context())

	fakeSched := model.NewFakeScheduler(0, errors.New("invalid"))
	limiter := limiter.New(ctx, slog.Default(), fakeSched, 1, 1, -1)
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

// TestLimiter_OnDeleteReturnsTokenForUnfinishedJob covers the eviction-shaped
// scenario: a previous controller was evicted, leaving behind unfinished k8s
// Jobs. The new controller takes tokens for them during informer cache sync
// (OnAdd), and those Jobs are later deleted without ever transitioning to
// finished. The tokens must be returned via OnDelete, or the limiter would
// leak capacity and eventually stop scheduling entirely.
func TestLimiter_OnDeleteReturnsTokenForUnfinishedJob(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	handled := make(chan string, 2)
	handler := handleFunc(func(_ context.Context, job *api.AgentScheduledJob) error {
		handled <- job.ID
		return nil
	})

	// maxInFlight=2, concurrency=1: the single worker holds one token while
	// waiting for work, leaving one token in the bucket.
	limiter := limiter.New(ctx, slog.Default(), handler, 2, 1, -1)

	// A pre-existing, unfinished job from a previous controller is discovered
	// during cache sync, taking the remaining token.
	staleJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: uuid.New().String()},
		},
		// No status conditions = not finished.
	}
	limiter.OnAdd(staleJob, true)

	// Enqueue two jobs. The worker's token covers the first, but the second
	// must wait for a token to be returned.
	job1 := &api.AgentScheduledJob{ID: uuid.New().String()}
	job2 := &api.AgentScheduledJob{ID: uuid.New().String()}
	if err := limiter.HandleMany(ctx, []*api.AgentScheduledJob{job1, job2}); err != nil {
		t.Errorf("limiter.HandleMany(ctx, jobs) = %v", err)
	}

	select {
	case got := <-handled:
		if got != job1.ID {
			t.Errorf("first handled job = %v, want %v", got, job1.ID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the first job to be handled")
	}

	// The bucket is empty (worker token + seeded token), so the second job
	// must not be handled yet.
	select {
	case got := <-handled:
		t.Fatalf("second job %v was handled before any token was returned", got)
	case <-time.After(100 * time.Millisecond):
	}

	// The stale unfinished job is deleted (e.g. cleaned up after the previous
	// controller's node was rotated). This must return its token, unblocking
	// the second job.
	limiter.OnDelete(staleJob)

	select {
	case got := <-handled:
		if got != job2.ID {
			t.Errorf("second handled job = %v, want %v", got, job2.ID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the second job to be handled: token was not returned on delete")
	}

	// Wait for the limiter workers to exit (to avoid logging after the test)
	cancel()
	limiter.Wait()
}
