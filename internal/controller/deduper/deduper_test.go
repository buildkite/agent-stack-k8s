package deduper_test

import (
	"context"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/google/uuid"
	"go.uber.org/zap/zaptest"
)

func TestDeduper_SkipsDuplicateJobs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &model.FakeScheduler{}
	dd := deduper.New(zaptest.NewLogger(t), handler)

	// Same job UUID for all calls.
	uuid := uuid.New().String()

	// The first Handle should succeed.
	if err := dd.Handle(ctx, model.Job{CommandJob: &api.CommandJob{Uuid: uuid}}); err != nil {
		t.Errorf("limiter.Handle(ctx, &job) = %v", err)
	}

	// The rest should fail.
	for range 49 {
		if err := dd.Handle(ctx, model.Job{CommandJob: &api.CommandJob{Uuid: uuid}}); err != model.ErrDuplicateJob {
			t.Errorf("limiter.Handle(ctx, &job) = %v, want %v", err, model.ErrDuplicateJob)
		}
	}

	handler.Wait()
	if got, want := len(handler.Running), 1; got != want {
		t.Errorf("len(handler.Running) = %d, want %d", got, want)
	}
	if got, want := len(handler.Finished), 0; got != want {
		t.Errorf("len(handler.Finished) = %d, want %d", got, want)
	}
	if got, want := handler.Errors, 0; got != want {
		t.Errorf("handler.Errors = %d, want %d", got, want)
	}
}
