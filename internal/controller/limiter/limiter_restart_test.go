package limiter_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// startRecorder records each job the limiter lets through, and never finishes
// it, so every start occupies a slot until the test ends.
type startRecorder struct {
	started chan *api.AgentScheduledJob
}

func (r *startRecorder) Handle(_ context.Context, job *api.AgentScheduledJob) error {
	r.started <- job
	return nil
}

func runningJob(id string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{config.UUIDLabel: id}},
	}
}

func finishedJob(id string) *batchv1.Job {
	j := runningJob(id)
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete}}
	return j
}

// expectStarts waits for exactly n starts, then checks no more arrive.
func expectStarts(t *testing.T, rec *startRecorder, n int) {
	t.Helper()
	for i := range n {
		select {
		case <-rec.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d starts, want %d", i, n)
		}
	}
	select {
	case j := <-rec.started:
		t.Fatalf("job %s started, but the limit allows only %d more", j.ID, n)
	case <-time.After(200 * time.Millisecond):
	}
}

// A controller that restarts with jobs still running must count them against
// max-in-flight, both while they run and after they finish.
func TestLimiter_Restart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inherited int
	}{
		{name: "inherited at the limit", inherited: 3},
		{name: "inherited over the limit", inherited: 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const maxInFlight = 3

			ctx, cancel := context.WithCancel(t.Context())
			rec := &startRecorder{started: make(chan *api.AgentScheduledJob, 100)}
			lim := limiter.New(ctx, slog.Default(), rec, maxInFlight, 4, -1)

			// The informer's initial list reports the previous controller's jobs.
			var inherited []string
			for range test.inherited {
				id := uuid.NewString()
				inherited = append(inherited, id)
				lim.OnAdd(runningJob(id), true)
			}
			lim.Start(ctx)

			var jobs []*api.AgentScheduledJob
			for range 10 {
				jobs = append(jobs, &api.AgentScheduledJob{ID: uuid.NewString()})
			}
			if err := lim.HandleMany(ctx, jobs); err != nil {
				t.Fatalf("HandleMany: %v", err)
			}

			// Every slot is taken by an inherited job, so nothing starts.
			expectStarts(t, rec, 0)

			// Each inherited job that finishes frees a slot only once the
			// number running is below the limit.
			running := test.inherited
			for _, id := range inherited {
				lim.OnUpdate(runningJob(id), finishedJob(id))
				running--
				want := 0
				if running < maxInFlight {
					want = 1
					running++
				}
				expectStarts(t, rec, want)
			}

			if running != maxInFlight {
				t.Errorf("running = %d, want %d", running, maxInFlight)
			}

			cancel()
			lim.Wait()
		})
	}
}

// During a rolling update the old controller can create jobs after the new
// one has synced. Those hold no token, so the new limiter must charge them.
func TestLimiter_JobFromAnotherController(t *testing.T) {
	t.Parallel()
	const maxInFlight = 2

	ctx, cancel := context.WithCancel(t.Context())
	rec := &startRecorder{started: make(chan *api.AgentScheduledJob, 100)}
	lim := limiter.New(ctx, slog.Default(), rec, maxInFlight, 4, -1)
	lim.Start(ctx)

	var jobs []*api.AgentScheduledJob
	for range 10 {
		jobs = append(jobs, &api.AgentScheduledJob{ID: uuid.NewString()})
	}
	if err := lim.HandleMany(ctx, jobs); err != nil {
		t.Fatalf("HandleMany: %v", err)
	}

	// The limiter's own jobs reach the informer too, and are not charged twice.
	var ours []string
	for range maxInFlight {
		j := <-rec.started
		ours = append(ours, j.ID)
		lim.OnAdd(runningJob(j.ID), false)
	}
	expectStarts(t, rec, 0)

	foreign := uuid.NewString()
	lim.OnAdd(runningJob(foreign), false)

	// Three running against a limit of two: the first to finish frees nothing.
	lim.OnUpdate(runningJob(ours[0]), finishedJob(ours[0]))
	expectStarts(t, rec, 0)
	lim.OnUpdate(runningJob(foreign), finishedJob(foreign))
	expectStarts(t, rec, 1)

	cancel()
	lim.Wait()
}

// A job deleted during a watch gap arrives as a tombstone, and still frees its
// slot.
func TestLimiter_DeletedFinalStateUnknown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	rec := &startRecorder{started: make(chan *api.AgentScheduledJob, 100)}
	lim := limiter.New(ctx, slog.Default(), rec, 1, 4, -1)

	id := uuid.NewString()
	lim.OnAdd(runningJob(id), true)
	lim.Start(ctx)

	if err := lim.HandleMany(ctx, []*api.AgentScheduledJob{{ID: uuid.NewString()}}); err != nil {
		t.Fatalf("HandleMany: %v", err)
	}
	expectStarts(t, rec, 0)

	lim.OnDelete(cache.DeletedFinalStateUnknown{Key: "buildkite/" + id, Obj: runningJob(id)})
	expectStarts(t, rec, 1)

	cancel()
	lim.Wait()
}
