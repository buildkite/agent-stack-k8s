package deduper_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

type handleFunc func(context.Context, *api.AgentScheduledJob) error

func (f handleFunc) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	return f(ctx, job)
}

func TestDeduper_SkipsDuplicateJobs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// No EventHandler: the "scheduled" job never finishes, so it remains
	// in-flight for the duration of the test.
	fakeSched := model.NewFakeScheduler(0, nil)
	dd := deduper.New(slog.Default(), fakeSched)

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

	if got, want := len(fakeSched.Running), 1; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
	if got, want := len(fakeSched.Finished), 0; got != want {
		t.Errorf("len(fakeSched.Finished) = %d, want %d", got, want)
	}
	if got, want := fakeSched.Errors, 0; got != want {
		t.Errorf("fakeSched.Errors = %d, want %d", got, want)
	}
}

func TestDeduper_ForgetsFinishedJobsOnUpdate(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	fakeSched := model.NewFakeScheduler(0, nil)
	dd := deduper.New(slog.Default(), fakeSched)

	id := uuid.New()

	unfinishedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: id.String()},
		},
		// No status conditions = not finished.
	}
	finishedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: id.String()},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}},
		},
	}

	// Simulate a controller restart: the deduper learns about a pre-existing,
	// still-unfinished k8s Job from the informer's initial list.
	dd.OnAdd(unfinishedJob, true)

	// While it remains unfinished, the job is considered in-flight, and
	// duplicates are rejected.
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != model.ErrDuplicateJob {
		t.Errorf("dd.Handle(ctx, &job) = %v, want %v", err, model.ErrDuplicateJob)
	}

	// An update that doesn't finish the job changes nothing.
	dd.OnUpdate(unfinishedJob, unfinishedJob)
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != model.ErrDuplicateJob {
		t.Errorf("dd.Handle(ctx, &job) = %v, want %v", err, model.ErrDuplicateJob)
	}

	// Once the job finishes, the deduper forgets it, and the same Buildkite
	// job (e.g. re-reserved after reservation expiry) is allowed through.
	dd.OnUpdate(unfinishedJob, finishedJob)
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != nil {
		t.Errorf("dd.Handle(ctx, &job) = %v, want nil", err)
	}

	if got, want := len(fakeSched.Running), 1; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
	if got, want := fakeSched.Errors, 0; got != want {
		t.Errorf("fakeSched.Errors = %d, want %d", got, want)
	}
}

func TestDeduper_IgnoresFinishedJobsInInitialList(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	fakeSched := model.NewFakeScheduler(0, nil)
	dd := deduper.New(slog.Default(), fakeSched)

	id := uuid.New()

	// Simulate a controller restart where the informer's initial list
	// contains a k8s Job that is already finished. It should not be tracked
	// as in-flight.
	dd.OnAdd(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: id.String()},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed}},
		},
	}, true)

	// The same Buildkite job is allowed through immediately.
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != nil {
		t.Errorf("dd.Handle(ctx, &job) = %v, want nil", err)
	}

	if got, want := len(fakeSched.Running), 1; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
	if got, want := fakeSched.Errors, 0; got != want {
		t.Errorf("fakeSched.Errors = %d, want %d", got, want)
	}
}

func TestDeduper_IgnoresUpdatesToAlreadyFinishedJobs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	handlerCalls := 0
	dd := deduper.New(slog.Default(), handleFunc(func(context.Context, *api.AgentScheduledJob) error {
		handlerCalls++
		return model.ErrDuplicateJob
	}))

	id := uuid.New()
	unfinishedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: id.String()},
		},
	}
	finishedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: id.String()},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}},
		},
	}

	dd.OnAdd(unfinishedJob, true)
	dd.OnUpdate(unfinishedJob, finishedJob)

	// The first retry reaches the scheduler, where the still-existing k8s Job
	// produces ErrDuplicateJob. The deduper deliberately marks it in-flight.
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != model.ErrDuplicateJob {
		t.Errorf("first dd.Handle(ctx, &job) = %v, want %v", err, model.ErrDuplicateJob)
	}

	// A metadata or status update that leaves the Job terminal must not clear
	// the marker established by the failed retry.
	dd.OnUpdate(finishedJob, finishedJob.DeepCopy())
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != model.ErrDuplicateJob {
		t.Errorf("second dd.Handle(ctx, &job) = %v, want %v", err, model.ErrDuplicateJob)
	}
	if got, want := handlerCalls, 1; got != want {
		t.Errorf("handler calls = %d, want %d", got, want)
	}
}

func TestDeduper_HandlesDeleteTombstone(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	fakeSched := model.NewFakeScheduler(0, nil)
	dd := deduper.New(slog.Default(), fakeSched)

	id := uuid.New()
	staleJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: id.String()},
		},
	}

	dd.OnAdd(staleJob, true)
	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != model.ErrDuplicateJob {
		t.Errorf("dd.Handle(ctx, &job) before deletion = %v, want %v", err, model.ErrDuplicateJob)
	}

	dd.OnDelete(cache.DeletedFinalStateUnknown{
		Key: "default/buildkite-" + id.String(),
		Obj: staleJob,
	})

	if err := dd.Handle(ctx, &api.AgentScheduledJob{ID: id.String()}); err != nil {
		t.Errorf("dd.Handle(ctx, &job) after deletion = %v, want nil", err)
	}
	if got, want := len(fakeSched.Running), 1; got != want {
		t.Errorf("len(fakeSched.Running) = %d, want %d", got, want)
	}
}
