package limiter_test

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"

	"github.com/google/uuid"
)

// Sorting the parameter slice never reorders the destination slice, at any
// capacity. Pins the Go-semantics premise behind the limiter fix.
func TestSortingSrcDoesNotReorderDst(t *testing.T) {
	check := func(t *testing.T, dstCap int) {
		t.Helper()

		dst := make([]*api.AgentScheduledJob, 2, dstCap)
		dst[0] = &api.AgentScheduledJob{ID: "dst-A", Priority: 10}
		dst[1] = &api.AgentScheduledJob{ID: "dst-B", Priority: 20}

		src := []*api.AgentScheduledJob{
			{ID: "src-X", Priority: 1},
			{ID: "src-Y", Priority: 99},
			{ID: "src-Z", Priority: 50},
		}

		dst = append(dst, src...)
		slices.SortStableFunc(src, func(a, b *api.AgentScheduledJob) int {
			return cmp.Compare(b.Priority, a.Priority)
		})

		gotDst := []int{dst[0].Priority, dst[1].Priority, dst[2].Priority, dst[3].Priority, dst[4].Priority}
		wantDst := []int{10, 20, 1, 99, 50}
		if !slices.Equal(gotDst, wantDst) {
			t.Errorf("dst = %v, want %v", gotDst, wantDst)
		}
	}

	t.Run("sufficient_capacity", func(t *testing.T) { check(t, 10) })
	t.Run("forced_realloc", func(t *testing.T) { check(t, 2) })
}

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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const (
		producers    = 16
		batchesEach  = 25
		jobsPerBatch = 50
		totalJobs    = producers * batchesEach * jobsPerBatch
	)

	rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, totalJobs)}
	lim := limiter.New(ctx, slog.Default(), rec, totalJobs, 8, totalJobs)

	var wg sync.WaitGroup
	for p := range producers {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(pid)))
			for b := range batchesEach {
				batch := make([]*api.AgentScheduledJob, 0, jobsPerBatch)
				for j := range jobsPerBatch {
					batch = append(batch, &api.AgentScheduledJob{
						ID:       fmt.Sprintf("p%d-b%d-j%d", pid, b, j),
						Priority: r.Intn(6),
					})
				}
				if err := lim.HandleMany(ctx, batch); err != nil {
					t.Errorf("HandleMany p%d b%d: %v", pid, b, err)
					return
				}
			}
		}(p)
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
