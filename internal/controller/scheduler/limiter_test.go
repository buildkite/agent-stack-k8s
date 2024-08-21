package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"

	"github.com/google/uuid"
	"go.uber.org/zap/zaptest"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeScheduler pretends to schedule jobs. In reality, it counts which jobs
// it "ran" and "finished".
type fakeScheduler struct {
	limiter *scheduler.MaxInFlightLimiter

	// configures the fake scheduler to complete jobs it creates
	completeJobs bool

	// configures the fake scheduler to return this error from Create
	err error

	mu       sync.Mutex
	wg       sync.WaitGroup
	running  []string
	finished []string
	errors   int
}

func (f *fakeScheduler) Create(_ context.Context, job *api.CommandJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		f.errors++
		return f.err
	}

	if slices.Contains(f.running, job.Uuid) {
		return fmt.Errorf("job %s already running", job.Uuid)
	}
	if slices.Contains(f.finished, job.Uuid) {
		return fmt.Errorf("job %s already finished", job.Uuid)
	}

	// The real scheduler doesn't peek into the limiter, this is just to verify
	// the limiter's behavior.
	if f.limiter.MaxInFlight > 0 && len(f.running) >= f.limiter.MaxInFlight {
		return fmt.Errorf("limit exceeded: len(running) = %d >= %d = f.limiter.MaxInFlight", len(f.running), f.limiter.MaxInFlight)
	}

	f.running = append(f.running, job.Uuid)

	if f.completeJobs {
		// Concurrently simulate the job completion.
		f.wg.Add(1)
		go f.complete(job.Uuid)
	}
	return nil
}

func (f *fakeScheduler) complete(uuid string) {
	f.mu.Lock()
	i := slices.Index(f.running, uuid)
	f.running = slices.Delete(f.running, i, i+1)
	f.finished = append(f.finished, uuid)
	f.mu.Unlock()

	f.limiter.OnUpdate(nil, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{config.UUIDLabel: uuid},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}},
		},
	})
	f.wg.Done()
}

func TestLimiter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &fakeScheduler{
		completeJobs: true,
	}
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 1)
	handler.limiter = limiter

	// simulate receiving a bunch of jobs
	var wg sync.WaitGroup
	wg.Add(50)
	for range 50 {
		go func() {
			defer wg.Done()
			err := limiter.Create(ctx, &api.CommandJob{Uuid: uuid.New().String()})
			if err != nil {
				t.Errorf("limiter.Create(ctx, &job) = %v", err)
			}
		}()
	}
	wg.Wait()

	handler.wg.Wait()
	if got, want := len(handler.running), 0; got != want {
		t.Errorf("len(handler.running) = %d, want %d", got, want)
	}
	if got, want := len(handler.finished), 50; got != want {
		t.Errorf("len(handler.finished) = %d, want %d", got, want)
	}
	if got, want := handler.errors, 0; got != want {
		t.Errorf("handler.errors = %d, want %d", got, want)
	}
}

func TestLimiter_SkipsDuplicateJobs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &fakeScheduler{
		completeJobs: false,
	}
	// no max-in-flight
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 0)
	handler.limiter = limiter

	// Same job UUID for all calls.
	uuid := uuid.New().String()

	for range 50 {
		if err := limiter.Create(ctx, &api.CommandJob{Uuid: uuid}); err != nil {
			t.Errorf("limiter.Create(ctx, &job) = %v", err)
		}
	}

	handler.wg.Wait()
	if got, want := len(handler.running), 1; got != want {
		t.Errorf("len(handler.running) = %d, want %d", got, want)
	}
	if got, want := len(handler.finished), 0; got != want {
		t.Errorf("len(handler.finished) = %d, want %d", got, want)
	}
	if got, want := handler.errors, 0; got != want {
		t.Errorf("handler.errors = %d, want %d", got, want)
	}
}

func TestLimiter_SkipsCreateErrors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := &fakeScheduler{
		err: errors.New("invalid"),
	}
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 1)
	handler.limiter = limiter

	for range 50 {
		err := limiter.Create(ctx, &api.CommandJob{Uuid: uuid.New().String()})
		if !errors.Is(err, handler.err) {
			t.Errorf("limiter.Create(ctx, some-job) error = %v, want %v", err, handler.err)
		}
	}

	handler.wg.Wait()
	if got, want := len(handler.running), 0; got != want {
		t.Errorf("len(handler.running) = %d, want %d", got, want)
	}
	if got, want := len(handler.finished), 0; got != want {
		t.Errorf("len(handler.finished) = %d, want %d", got, want)
	}
	if got, want := handler.errors, 50; got != want {
		t.Errorf("handler.errors = %d, want %d", got, want)
	}
}
