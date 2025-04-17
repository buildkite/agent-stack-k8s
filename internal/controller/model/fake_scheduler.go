package model

import (
	"context"
	"fmt"
	"sync"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// FakeScheduler pretends to schedule jobs. In reality, it counts which jobs
// it "ran" and "finished".
type FakeScheduler struct {
	// EventHandler provides the fake scheduler with a handler to complete the
	// "jobs" it creates with a k8s JobComplete event.
	EventHandler cache.ResourceEventHandler

	// Err configures the fake scheduler to return this error from Handle.
	Err error

	// MaxRunning configures the fake scheduler to start returning errors if
	// len(Running) >= MaxRunning (scheduling another job would exceed the
	// limit).
	MaxRunning int

	mu       sync.Mutex
	wg       sync.WaitGroup
	Running  map[string]struct{}
	Finished map[string]struct{}
	Errors   int
}

func NewFakeScheduler(maxRunning int, err error) *FakeScheduler {
	return &FakeScheduler{
		MaxRunning: maxRunning,
		Err:        err,
		Running:    make(map[string]struct{}),
		Finished:   make(map[string]struct{}),
	}
}

func (f *FakeScheduler) Handle(_ context.Context, job *api.AgentScheduledJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		f.Errors++
		f.wg.Done()
		return f.Err
	}

	if _, running := f.Running[job.ID]; running {
		return fmt.Errorf("job %s already running", job.ID)
	}
	if _, finished := f.Finished[job.ID]; finished {
		return fmt.Errorf("job %s already finished", job.ID)
	}

	if f.MaxRunning > 0 && len(f.Running) >= f.MaxRunning {
		return fmt.Errorf("limit exceeded: len(f.Running) = %d >= %d = f.MaxRunning", len(f.Running), f.MaxRunning)
	}

	f.Running[job.ID] = struct{}{}

	if f.EventHandler != nil {
		// Concurrently simulate the job completion.
		go f.complete(job.ID)
	}
	return nil
}

func (f *FakeScheduler) complete(uuid string) {
	f.mu.Lock()
	delete(f.Running, uuid)
	f.Finished[uuid] = struct{}{}
	f.mu.Unlock()

	f.EventHandler.OnUpdate(
		// Previous state
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{config.UUIDLabel: uuid},
			},
			// No status conditions
		},
		// New state
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{config.UUIDLabel: uuid},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}},
			},
		},
	)
	f.wg.Done()
}

// Add adds n to the fake work waitgroup.
func (f *FakeScheduler) Add(n int) { f.wg.Add(n) }

// Wait waits for all fake work to complete. Call this before inspecting
// Running, Finished, and Errors.
func (f *FakeScheduler) Wait() { f.wg.Wait() }
