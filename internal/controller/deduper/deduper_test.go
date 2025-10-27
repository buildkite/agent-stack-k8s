package deduper_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/google/uuid"
)

func TestDeduper_SkipsDuplicateJobs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	fakeSched := model.NewFakeScheduler(0, nil)
	dd := deduper.New(slog.Default(), fakeSched)
	fakeSched.EventHandler = dd
	fakeSched.Add(1) // only 1 job should get through

	// Same job UUID for all calls.
	uuid := uuid.New().String()

	// The first Handle should succeed.
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: uuid}); err != nil {
		t.Errorf("dd.Handle(ctx, &job) = %v", err)
	}

	// The rest should fail.
	for range 49 {
		if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: uuid}); err != model.ErrDuplicateJob {
			t.Errorf("dd.Handle(ctx, &job) = %v, want %v", err, model.ErrDuplicateJob)
		}
	}

	fakeSched.Wait()

	if got, want := len(fakeSched.Running), 0; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
	if got, want := len(fakeSched.Finished), 1; got != want {
		t.Errorf("len(fakeSched.Finished) = %d, want %d", got, want)
	}
	if got, want := fakeSched.Errors, 0; got != want {
		t.Errorf("fakeSched.Errors = %d, want %d", got, want)
	}
}
