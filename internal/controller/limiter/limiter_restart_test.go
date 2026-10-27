package limiter_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/prometheus/client_golang/prometheus"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func runningJob(id string) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{config.UUIDLabel: id}}}
}

func finishedJob(id string) *batchv1.Job {
	j := runningJob(id)
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete}}
	return j
}

// Wait for all workers to block, then check dispatches without timing sleeps.
func expectStarts(t *testing.T, out <-chan *api.AgentScheduledJob, want int) []*api.AgentScheduledJob {
	t.Helper()
	synctest.Wait()
	var jobs []*api.AgentScheduledJob
	for {
		select {
		case j := <-out:
			jobs = append(jobs, j)
		default:
			if len(jobs) != want {
				t.Fatalf("started %d jobs, want %d", len(jobs), want)
			}
			return jobs
		}
	}
}

func TestLimiterRestart(t *testing.T) {
	for _, inherited := range []int{1, 3, 5} {
		t.Run(fmt.Sprintf("inherited=%d", inherited), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				var existing []runtime.Object
				for i := range inherited {
					j := runningJob(fmt.Sprintf("inherited-%d", i))
					j.Name, j.Namespace = fmt.Sprintf("job-%d", i), "test"
					existing = append(existing, j)
				}
				client := fake.NewClientset(existing...)
				factory := informers.NewSharedInformerFactory(client, 0)
				rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, 10)}
				l := limiter.New(ctx, slog.Default(), rec, 3, 4, -1)
				defer func() { cancel(); l.Wait(); factory.Shutdown() }()
				// Replay a warm cache, as happens after the deduper starts the
				// shared informer in the controller's real startup sequence.
				jobInformer := factory.Batch().V1().Jobs().Informer()
				factory.Start(ctx.Done())
				if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
					t.Fatal("informer did not sync")
				}
				if err := l.RegisterInformer(ctx, factory); err != nil {
					t.Fatal(err)
				}
				var queued []*api.AgentScheduledJob
				for i := range 10 {
					queued = append(queued, &api.AgentScheduledJob{ID: fmt.Sprintf("queued-%d", i)})
				}
				if err := l.HandleMany(ctx, queued); err != nil {
					t.Fatal(err)
				}
				expectStarts(t, rec.out, max(0, 3-inherited))
				for i := range inherited {
					id := fmt.Sprintf("inherited-%d", i)
					j := finishedJob(id)
					j.Name, j.Namespace = fmt.Sprintf("job-%d", i), "test"
					if _, err := client.BatchV1().Jobs("test").UpdateStatus(ctx, j, metav1.UpdateOptions{}); err != nil {
						t.Fatal(err)
					}
					want := 0
					if inherited-i-1 < 3 {
						want = 1
					}
					expectStarts(t, rec.out, want)
				}
			})
		})
	}
}

func TestLimiterForeignJobBeforeQueuedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, 10)}
		l := limiter.New(ctx, slog.Default(), rec, 1, 1, -1)
		defer func() { cancel(); l.Wait() }()
		synctest.Wait() // The worker is idle before the foreign job appears.
		l.OnAdd(runningJob("foreign"), false)
		if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "local"}}); err != nil {
			t.Fatal(err)
		}
		expectStarts(t, rec.out, 0)
		l.OnUpdate(runningJob("foreign"), finishedJob("foreign"))
		expectStarts(t, rec.out, 1)
	})
}

func TestLimiterForeignJobWhileFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, 10)}
		l := limiter.New(ctx, slog.Default(), rec, 2, 4, -1)
		defer func() { cancel(); l.Wait() }()
		if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "one"}, {ID: "two"}, {ID: "three"}, {ID: "four"}}); err != nil {
			t.Fatal(err)
		}
		ours := expectStarts(t, rec.out, 2)
		for _, j := range ours {
			l.OnAdd(runningJob(j.ID), false)
		}
		l.OnAdd(runningJob("foreign"), false)
		l.OnUpdate(runningJob(ours[0].ID), finishedJob(ours[0].ID))
		expectStarts(t, rec.out, 0) // Still two unfinished jobs against a limit of two.
		l.OnUpdate(runningJob("foreign"), finishedJob("foreign"))
		expectStarts(t, rec.out, 1)
		l.OnUpdate(runningJob(ours[1].ID), finishedJob(ours[1].ID))
		expectStarts(t, rec.out, 1)
	})
}

func TestLimiterCapacityWaitMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rec := &drainOrderHandler{out: make(chan *api.AgentScheduledJob, 10)}
		l := limiter.New(ctx, slog.Default(), rec, 1, 1, -1)
		defer func() { cancel(); l.Wait() }()
		readWait := func() (uint64, float64) {
			t.Helper()
			families, err := prometheus.DefaultGatherer.Gather()
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range families {
				if f.GetName() == "buildkite_limiter_token_wait_duration_seconds" {
					h := f.GetMetric()[0].GetHistogram()
					return h.GetSampleCount(), h.GetSampleSum()
				}
			}
			t.Fatal("capacity-wait histogram not found")
			return 0, 0
		}
		if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "first"}}); err != nil {
			t.Fatal(err)
		}
		current := expectStarts(t, rec.out, 1)[0]
		for i, tc := range []struct {
			polls []time.Duration
			want  float64
		}{
			{polls: []time.Duration{10 * time.Second, 20 * time.Second, 35 * time.Second}, want: 65},
			{polls: []time.Duration{7 * time.Second, 11 * time.Second}, want: 18},
		} {
			count, sum := readWait()
			next := &api.AgentScheduledJob{ID: fmt.Sprintf("next-%d", i)}
			for _, interval := range tc.polls {
				time.Sleep(interval)
				if err := l.HandleMany(ctx, []*api.AgentScheduledJob{next}); err != nil {
					t.Fatal(err)
				}
				expectStarts(t, rec.out, 0)
				if gotCount, gotSum := readWait(); gotCount != count || gotSum != sum {
					t.Fatalf("unsuccessful poll recorded capacity wait: count/sum = %d/%g, want %d/%g", gotCount, gotSum, count, sum)
				}
			}
			l.OnUpdate(runningJob(current.ID), finishedJob(current.ID))
			current = expectStarts(t, rec.out, 1)[0]
			if gotCount, gotSum := readWait(); gotCount != count+1 || gotSum != sum+tc.want {
				t.Fatalf("capacity acquisition count/sum = %d/%g, want %d/%g", gotCount, gotSum, count+1, sum+tc.want)
			}
		}
		count, sum := readWait()
		time.Sleep(5 * time.Second)
		cancel()
		l.Wait()
		if gotCount, gotSum := readWait(); gotCount != count || gotSum != sum {
			t.Fatalf("cancellation recorded capacity wait: count/sum = %d/%g, want %d/%g", gotCount, gotSum, count, sum)
		}
	})
}

type schedulingHandler func(context.Context, *api.AgentScheduledJob) error

func (f schedulingHandler) Handle(ctx context.Context, j *api.AgentScheduledJob) error {
	return f(ctx, j)
}

func TestLimiterSchedulingNotifications(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		notifyLate bool
		terminal   bool
		deleted    bool
	}{
		{name: "duplicate before notification", err: model.ErrDuplicateJob, notifyLate: true},
		{name: "duplicate after notification", err: model.ErrDuplicateJob},
		{name: "failure after notification", err: errors.New("scheduling failed")},
		{name: "completed before success", terminal: true},
		{name: "completed before failure", terminal: true, err: errors.New("scheduling failed")},
		{name: "deleted before failure", deleted: true, err: errors.New("scheduling failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				release := make(chan struct{})
				out := make(chan *api.AgentScheduledJob, 10)
				h := schedulingHandler(func(_ context.Context, j *api.AgentScheduledJob) error {
					if j.ID == "first" {
						<-release
						return tc.err
					}
					out <- j
					return nil
				})
				l := limiter.New(ctx, slog.Default(), h, 1, 2, -1)
				defer func() { cancel(); l.Wait() }()
				if err := l.HandleMany(ctx, []*api.AgentScheduledJob{{ID: "first"}, {ID: "second"}, {ID: "third"}}); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if tc.notifyLate {
					// Hold dispatch while the failed attempt is released, then
					// report the foreign job before allowing fresh work again.
					l.Pause(true)
					close(release)
					expectStarts(t, out, 0)
				}
				if tc.terminal {
					// The first notification can already contain the terminal state.
					l.OnAdd(finishedJob("first"), false)
				} else {
					l.OnAdd(runningJob("first"), false)
				}
				if tc.deleted {
					l.OnDelete(cache.DeletedFinalStateUnknown{Obj: runningJob("first")})
				}
				if !tc.notifyLate {
					close(release)
				} else {
					l.Pause(false)
				}
				if !tc.terminal && !tc.deleted {
					expectStarts(t, out, 0)
					l.OnUpdate(runningJob("first"), finishedJob("first"))
				}
				started := expectStarts(t, out, 1)
				// Repeated completion and deletion must not free a second slot.
				if !tc.deleted {
					l.OnUpdate(finishedJob("first"), finishedJob("first"))
					l.OnDelete(finishedJob("first"))
				}
				expectStarts(t, out, 0)
				l.OnDelete(cache.DeletedFinalStateUnknown{Obj: runningJob(started[0].ID)})
				expectStarts(t, out, 1)
			})
		})
	}
}
