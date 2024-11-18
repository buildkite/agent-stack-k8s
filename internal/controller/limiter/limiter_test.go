package limiter_test

import (
	"context"
	"errors"
	"sync"
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
	defer cancel()

	handler := &model.FakeScheduler{
		MaxRunning: 1,
	}
	limiter := limiter.New(zaptest.NewLogger(t), handler, 1)
	handler.EventHandler = limiter

	// simulate receiving a bunch of jobs
	var wg sync.WaitGroup
	wg.Add(50)
	for range 50 {
		go func() {
			defer wg.Done()
			err := limiter.Handle(ctx, model.Job{CommandJob: &api.CommandJob{Uuid: uuid.New().String()}})
			if err != nil {
				t.Errorf("limiter.Handle(ctx, &job) = %v", err)
			}
		}()
	}
	wg.Wait()

	handler.Wait()

	if got, want := len(handler.Running), 0; got != want {
		t.Errorf("len(handler.running) = %d, want %d", got, want)
	}
	if got, want := len(handler.Finished), 50; got != want {
		t.Errorf("len(handler.finished) = %d, want %d", got, want)
	}
	if got, want := handler.Errors, 0; got != want {
		t.Errorf("handler.errors = %d, want %d", got, want)
	}
}

func TestLimiter_SkipsCreateErrors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &model.FakeScheduler{
		Err: errors.New("invalid"),
	}
	limiter := limiter.New(zaptest.NewLogger(t), handler, 1)
	handler.EventHandler = limiter

	for range 50 {
		err := limiter.Handle(ctx, model.Job{CommandJob: &api.CommandJob{Uuid: uuid.New().String()}})
		if !errors.Is(err, handler.Err) {
			t.Errorf("limiter.Handle(ctx, some-job) error = %v, want %v", err, handler.Err)
		}
	}

	handler.Wait()
	if got, want := len(handler.Running), 0; got != want {
		t.Errorf("len(handler.running) = %d, want %d", got, want)
	}
	if got, want := len(handler.Finished), 0; got != want {
		t.Errorf("len(handler.finished) = %d, want %d", got, want)
	}
	if got, want := handler.Errors, 50; got != want {
		t.Errorf("handler.errors = %d, want %d", got, want)
	}
}
