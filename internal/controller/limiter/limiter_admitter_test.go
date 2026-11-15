package limiter_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
)

type fakeAdmitter struct {
	mu       sync.Mutex
	room     int
	attempts int
	released []string
}

func (a *fakeAdmitter) TryAdmit(string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts++
	if a.room == 0 {
		return false
	}
	a.room--
	return true
}

func (a *fakeAdmitter) Release(jobID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.released = append(a.released, jobID)
}

func (a *fakeAdmitter) addRoom(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.room += n
}

func TestLimiterAdmitterHoldsRefusedJobsUntilRecheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var mu sync.Mutex
		var dispatched []string
		l := limiter.New(ctx, slog.Default(), schedulingHandler(func(_ context.Context, job *api.AgentScheduledJob) error {
			mu.Lock()
			defer mu.Unlock()
			dispatched = append(dispatched, job.ID)
			return nil
		}), 0, 4, 10)
		admitter := &fakeAdmitter{room: 1}
		l.SetAdmitter(admitter)

		if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "a"}, {ID: "b"}, {ID: "c"}}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got, want := dispatched, []string{"a"}; !slices.Equal(got, want) {
			t.Fatalf("dispatched before recheck = %v, want %v", got, want)
		}
		// A refusal parks every worker: no further attempts until Recheck.
		if got, want := admitter.attempts, 2; got != want {
			t.Fatalf("admission attempts before recheck = %d, want %d", got, want)
		}

		admitter.addRoom(2)
		l.Recheck()
		synctest.Wait()
		// Concurrent workers may dispatch b and c in either order.
		slices.Sort(dispatched)
		if got, want := dispatched, []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Errorf("dispatched after recheck = %v, want %v", got, want)
		}

		cancel()
		l.Wait()
	})
}

func TestLimiterAdmitterReleasesFailedDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		l := limiter.New(ctx, slog.Default(), schedulingHandler(func(context.Context, *api.AgentScheduledJob) error {
			return errors.New("reservation lost")
		}), 0, 1, 10)
		admitter := &fakeAdmitter{room: 1}
		l.SetAdmitter(admitter)

		if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "a"}}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got, want := admitter.released, []string{"a"}; !slices.Equal(got, want) {
			t.Errorf("released = %v, want %v", got, want)
		}

		cancel()
		l.Wait()
	})
}
