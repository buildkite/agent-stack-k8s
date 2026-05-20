package limiter_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"

	"github.com/google/uuid"
)

type drainOrderHandler struct {
	out chan *api.AgentScheduledJob
}

func (d *drainOrderHandler) Handle(_ context.Context, job *api.AgentScheduledJob) error {
	d.out <- job
	return nil
}

// A high-priority job in a mixed batch should drain first.
func TestHandleMany_SortsBatchByPriority(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	const n = 10
	rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, n)}
	lim := limiter.New(ctx, slog.Default(), rec, n, 1, -1)

	jobs := []*api.AgentScheduledJob{
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 99},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
		{ID: uuid.NewString(), Priority: 0},
	}
	highID := jobs[5].ID

	if err := lim.HandleMany(ctx, jobs); err != nil {
		t.Fatalf("HandleMany: %v", err)
	}

	for i := range n {
		j := <-rec.out
		if j.ID == highID {
			if i != 0 {
				t.Errorf("priority=99 drained at position %d, want 0", i)
			}
			return
		}
	}
	t.Errorf("priority=99 job not observed")
}

// A high-priority job in a later batch should drain ahead of low-priority
// jobs that arrived earlier.
func TestHandleMany_SortsAcrossBatches(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	const lowN = 200
	rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, lowN+1)}
	lim := limiter.New(ctx, slog.Default(), rec, lowN+1, 1, lowN+10)

	batchA := make([]*api.AgentScheduledJob, 0, lowN)
	for range lowN {
		batchA = append(batchA, &api.AgentScheduledJob{ID: uuid.NewString(), Priority: 0})
	}
	if err := lim.HandleMany(ctx, batchA); err != nil {
		t.Fatalf("HandleMany A: %v", err)
	}

	highID := uuid.NewString()
	if err := lim.HandleMany(ctx, []*api.AgentScheduledJob{{ID: highID, Priority: 5}}); err != nil {
		t.Fatalf("HandleMany B: %v", err)
	}

	first := <-rec.out
	if first.ID != highID {
		t.Errorf("first drained job = %s, want high-priority %s", first.ID, highID)
	}
}

// When the queue exceeds WorkQueueLimit, low-priority jobs should be the ones
// truncated, not the most recently appended.
func TestHandleMany_WorkQueueLimitDropsByPriority(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	const (
		lowN     = 100
		highN    = 50
		queueCap = 100
		maxFlt   = lowN + highN
	)

	rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, maxFlt)}
	lim := limiter.New(ctx, slog.Default(), rec, maxFlt, 1, queueCap)
	lim.Pause(true)

	highIDs := make(map[string]bool, highN)
	batchA := make([]*api.AgentScheduledJob, 0, lowN)
	for range lowN {
		batchA = append(batchA, &api.AgentScheduledJob{ID: uuid.NewString(), Priority: 0})
	}
	if err := lim.HandleMany(ctx, batchA); err != nil {
		t.Fatalf("HandleMany A: %v", err)
	}

	batchB := make([]*api.AgentScheduledJob, 0, highN)
	for range highN {
		id := uuid.NewString()
		batchB = append(batchB, &api.AgentScheduledJob{ID: id, Priority: 5})
		highIDs[id] = true
	}
	if err := lim.HandleMany(ctx, batchB); err != nil {
		t.Fatalf("HandleMany B: %v", err)
	}

	lim.Pause(false)

	highDrained := 0
	for range queueCap {
		j := <-rec.out
		if highIDs[j.ID] {
			highDrained++
		}
	}
	if highDrained != highN {
		t.Errorf("priority=5 drained: %d, want %d", highDrained, highN)
	}
}

// Concurrent HandleMany callers + multiple workers must not race or lose
// jobs. Run with -race.
func TestHandleMany_ConcurrentProducersRaceFree(t *testing.T) {
	ctx := t.Context()

	const (
		producers    = 16
		batchesEach  = 25
		jobsPerBatch = 50
		totalJobs    = producers * batchesEach * jobsPerBatch
	)

	rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, totalJobs)}
	lim := limiter.New(ctx, slog.Default(), rec, totalJobs, 8, totalJobs)

	var wg sync.WaitGroup
	for pid := range producers {
		wg.Go(func() {
			for b := range batchesEach {
				batch := make([]*api.AgentScheduledJob, 0, jobsPerBatch)
				for j := range jobsPerBatch {
					batch = append(batch, &api.AgentScheduledJob{
						ID:       fmt.Sprintf("p%d-b%d-j%d", pid, b, j),
						Priority: rand.N(6),
					})
				}
				if err := lim.HandleMany(ctx, batch); err != nil {
					t.Errorf("HandleMany p%d b%d: %v", pid, b, err)
					return
				}
			}
		})
	}
	wg.Wait()

	got := 0
	timeout := time.After(30 * time.Second)
	for got < totalJobs {
		select {
		case <-rec.out:
			got++
		case <-timeout:
			t.Fatalf("timeout: drained %d of %d", got, totalJobs)
		}
	}
}
