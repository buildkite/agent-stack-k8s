package limiter_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/google/uuid"
)

func TestLimiterDrainDeadline(t *testing.T) {
	for _, ignoresCancellation := range []bool{false, true} {
		t.Run(fmt.Sprint(ignoresCancellation), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				release := make(chan struct{})
				var dispatchCtx context.Context
				var starts int
				l := limiter.New(ctx, slog.Default(), schedulingHandler(func(ctx context.Context, _ *api.AgentScheduledJob) error {
					starts++
					dispatchCtx = ctx
					if ignoresCancellation {
						<-release
					} else {
						<-ctx.Done()
					}
					return ctx.Err()
				}), 0, 1, 10)
				if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "accepted"}, {ID: "queued"}}); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				cancel()
				if dispatchCtx == nil || dispatchCtx.Err() != nil {
					t.Fatal("shutdown canceled accepted dispatch before the drain deadline")
				}
				if err := l.HandleMany(context.Background(), []*api.AgentScheduledJob{{ID: "late"}}); !errors.Is(err, context.Canceled) {
					t.Fatalf("late admission error = %v, want context.Canceled", err)
				}
				drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer drainCancel()
				start := time.Now()
				if err := l.Drain(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Drain() = %v, want deadline exceeded", err)
				}
				if time.Since(start) != 20*time.Second || dispatchCtx.Err() != context.Canceled {
					t.Fatal("drain did not cancel dispatch at the deadline")
				}
				close(release)
				l.Wait()
				if starts != 1 {
					t.Errorf("started %d dispatches, want only the accepted job", starts)
				}
			})
		})
	}
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

	for _, schedulingErr := range []error{errors.New("invalid"), model.ErrDuplicateJob, model.ErrStaleJob} {
		t.Run(schedulingErr.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())

			fakeSched := model.NewFakeScheduler(0, schedulingErr)
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
		})
	}
}
