package jatissuer

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeClient struct {
	mu        sync.Mutex
	responses []*api.IssueJobAcquisitionTokensResponse
	errors    []error
	calls     []fakeClientCall
}

type fakeClientCall struct {
	ids                  []string
	tokenLifetimeSeconds int
}

func (f *fakeClient) IssueJobAcquisitionTokens(_ context.Context, ids []string, tokenLifetimeSeconds int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeClientCall{ids: ids, tokenLifetimeSeconds: tokenLifetimeSeconds})
	i := len(f.calls) - 1
	var resp *api.IssueJobAcquisitionTokensResponse
	if i < len(f.responses) {
		resp = f.responses[i]
	}
	var err error
	if i < len(f.errors) {
		err = f.errors[i]
	}
	return resp, 0, err
}

type captureHandler struct {
	jobs chan *api.AgentScheduledJob
}

func (h *captureHandler) Handle(_ context.Context, job *api.AgentScheduledJob) error {
	h.jobs <- job
	return nil
}

func jobStates(id string) (*batchv1.Job, *batchv1.Job) {
	meta := metav1.ObjectMeta{Labels: map[string]string{config.UUIDLabel: id}}
	return &batchv1.Job{ObjectMeta: meta}, &batchv1.Job{
		ObjectMeta: meta,
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}}},
	}
}

func TestHandleCorrelatesIssuedTokenByJobUUID(t *testing.T) {
	t.Parallel()

	jobID := uuid.NewString()
	tests := []struct {
		name string
		resp *api.IssueJobAcquisitionTokensResponse
	}{
		{name: "not issued", resp: &api.IssueJobAcquisitionTokensResponse{NotIssued: []string{jobID}}},
		{name: "missing", resp: &api.IssueJobAcquisitionTokensResponse{}},
		{name: "mismatched", resp: &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: uuid.NewString(), JobAcquisitionToken: "wrong"}}}},
		{name: "duplicate", resp: &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: jobID, JobAcquisitionToken: "one"}, {JobUUID: jobID, JobAcquisitionToken: "two"}}}},
		{name: "empty token", resp: &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: jobID}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 1)}
			h := New(t.Context(), slog.Default(), &fakeClient{responses: []*api.IssueJobAcquisitionTokensResponse{test.resp}}, next, 0)
			if err := h.Handle(t.Context(), &api.AgentScheduledJob{ID: jobID}); err == nil {
				t.Fatal("Handle() error = nil, want non-nil")
			}
			select {
			case <-next.jobs:
				t.Fatal("next handler called for malformed issuance response")
			default:
			}
		})
	}
}

func TestHandleRetriesWithConfiguredLifetimeAndForwardsTokenThroughDeduper(t *testing.T) {
	t.Parallel()

	jobID := uuid.NewString()
	client := &fakeClient{
		responses: []*api.IssueJobAcquisitionTokensResponse{nil, {
			JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: jobID, JobAcquisitionToken: "jat-secret"}},
		}},
		errors: []error{errors.New("temporary failure"), nil},
	}
	next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 1)}
	h := New(t.Context(), slog.Default(), client, deduper.New(slog.Default(), next), 1800)

	if err := h.Handle(t.Context(), &api.AgentScheduledJob{ID: jobID}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	got := <-next.jobs
	if got.JobAcquisitionToken != "jat-secret" {
		t.Errorf("JobAcquisitionToken = %q, want %q", got.JobAcquisitionToken, "jat-secret")
	}
	if got := len(client.calls); got != 2 {
		t.Errorf("issuance calls = %d, want 2", got)
	}
	for i, call := range client.calls {
		if len(call.ids) != 1 || call.ids[0] != jobID {
			t.Errorf("issuance call %d job IDs = %v, want [%s]", i, call.ids, jobID)
		}
		if call.tokenLifetimeSeconds != 1800 {
			t.Errorf("issuance call %d token lifetime = %d, want 1800", i, call.tokenLifetimeSeconds)
		}
	}
}

func TestIssuerRunsOnlyAfterLimiterReleasesCapacity(t *testing.T) {
	job1 := uuid.NewString()
	job2 := uuid.NewString()
	client := &fakeClient{responses: []*api.IssueJobAcquisitionTokensResponse{
		{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: job1, JobAcquisitionToken: "jat-1"}}},
		{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: job2, JobAcquisitionToken: "jat-2"}}},
	}}
	next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 2)}
	ctx, cancel := context.WithCancel(t.Context())
	h := New(ctx, slog.Default(), client, next, 0)
	lim := limiter.New(ctx, slog.Default(), h, 1, 1, 10)
	defer lim.Wait()
	defer cancel()

	if err := lim.HandleMany(ctx, []*api.AgentScheduledJob{{ID: job1}, {ID: job2}}); err != nil {
		t.Fatalf("HandleMany() error = %v", err)
	}
	first := <-next.jobs
	client.mu.Lock()
	gotCalls := len(client.calls)
	client.mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("issuance calls before capacity release = %d, want 1", gotCalls)
	}

	prev, curr := jobStates(first.ID)
	lim.OnUpdate(prev, curr)
	second := <-next.jobs
	if second.ID != job2 {
		t.Errorf("second job ID = %q, want %q", second.ID, job2)
	}
	cancel()
}

type clientFunc func(context.Context, []string, int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error)

func (f clientFunc) IssueJobAcquisitionTokens(ctx context.Context, ids []string, lifetime int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
	return f(ctx, ids, lifetime)
}

func TestBatchedJobsRecoverBeyondOldRetryBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var calls []time.Time
		client := clientFunc(func(_ context.Context, ids []string, lifetime int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
			calls = append(calls, time.Now())
			if len(ids) != 25 || lifetime != 900 {
				t.Errorf("request = %d IDs, lifetime %d, want 25 IDs and 900 seconds", len(ids), lifetime)
			}
			if len(calls) <= 6 {
				return nil, 3 * time.Second, api.AgentError{StatusCode: 429, Message: "throttled"}
			}
			response := &api.IssueJobAcquisitionTokensResponse{}
			// Deliberately reverse the order to test UUID correlation.
			for j := len(ids) - 1; j >= 0; j-- {
				response.JobAcquisitionTokens = append(response.JobAcquisitionTokens, api.IssuedJobAcquisitionToken{
					JobUUID: ids[j], JobAcquisitionToken: api.JobAcquisitionToken("token-" + ids[j]),
				})
			}
			return response, 0, nil
		})
		next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 50)}
		h := New(ctx, slog.Default(), client, deduper.New(slog.Default(), next), 900)
		l := limiter.New(ctx, slog.Default(), h, 25, 25, 100)
		defer l.Wait()
		defer cancel()
		jobs := make([]*api.AgentScheduledJob, 25)
		for j := range jobs {
			jobs[j] = &api.AgentScheduledJob{ID: uuid.NewString()}
		}
		start := time.Now()
		if err := l.HandleMany(ctx, jobs); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		// Repeated polling must not create a second scheduling attempt while
		// the first still holds its capacity slot and retries token issuance.
		if err := l.HandleMany(ctx, jobs); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if len(calls) != 7 || len(next.jobs) != 25 {
			t.Fatalf("%d token requests, %d dispatched jobs; want 7 requests and all 25 jobs", len(calls), len(next.jobs))
		}
		for j := 1; j < len(calls); j++ {
			if delta := calls[j].Sub(calls[j-1]); delta < 3*time.Second {
				t.Errorf("retry interval = %s, below Retry-After of 3s", delta)
			}
		}
		if elapsed := calls[6].Sub(start); elapsed >= 900*time.Second {
			t.Errorf("recovery took %s, want recovery within reservation lifetime", elapsed)
		}
		seen := make(map[string]bool)
		for range 25 {
			job := <-next.jobs
			if seen[job.ID] || job.JobAcquisitionToken != api.JobAcquisitionToken("token-"+job.ID) {
				t.Errorf("duplicate dispatch or wrong token for %s", job.ID)
			}
			seen[job.ID] = true
		}
	})
}

func TestSharedRequestRateIncludesRetriesAndNewJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var calls []time.Time
		client := clientFunc(func(_ context.Context, ids []string, _ int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
			calls = append(calls, time.Now())
			if len(calls) == 2 {
				return nil, 0, api.AgentError{StatusCode: 503, Message: "unavailable"}
			}
			return &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{
				{JobUUID: ids[0], JobAcquisitionToken: "token"},
			}}, 0, nil
		})
		next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 12)}
		h := New(ctx, slog.Default(), client, next, 0)
		for range 12 {
			if err := h.Handle(ctx, &api.AgentScheduledJob{ID: uuid.NewString()}); err != nil {
				t.Fatal(err)
			}
		}
		if len(calls) != 13 || len(next.jobs) != 12 {
			t.Fatalf("%d requests, %d dispatched jobs; want 13 and 12", len(calls), len(next.jobs))
		}
		for j := 1; j < len(calls); j++ {
			if delta := calls[j].Sub(calls[j-1]); delta < 100*time.Millisecond {
				t.Errorf("requests %d and %d separated by %s, want at least 100ms", j-1, j, delta)
			}
		}
	})
}

func TestTerminalFailuresDoNotBlockLaterJobs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		cancel bool
	}{
		{name: "permanent rejection", status: http.StatusForbidden},
		{name: "reservation expiry", status: http.StatusTooManyRequests},
		{name: "cancellation before expiry", status: http.StatusTooManyRequests, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				lifetime := time.Second
				if tc.cancel {
					lifetime = time.Hour
				}
				jobCtx, cancel := context.WithTimeout(ctx, lifetime)
				defer cancel()
				if tc.cancel {
					time.AfterFunc(time.Second, cancel)
				}
				calls := 0
				client := clientFunc(func(_ context.Context, ids []string, _ int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
					calls++
					if calls == 1 {
						return nil, time.Hour, api.AgentError{StatusCode: tc.status, Message: "rejected"}
					}
					return &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{
						{JobUUID: ids[0], JobAcquisitionToken: "token"},
					}}, 0, nil
				})
				next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 2)}
				h := New(ctx, slog.Default(), client, next, 0)
				start := time.Now()
				if err := h.Handle(jobCtx, &api.AgentScheduledJob{ID: "rejected"}); err == nil {
					t.Fatal("rejected or expired job returned no error")
				}
				if err := h.Handle(ctx, &api.AgentScheduledJob{ID: "eligible"}); err != nil {
					t.Fatal(err)
				}
				if calls != 2 || len(next.jobs) != 1 || (<-next.jobs).ID != "eligible" {
					t.Fatalf("requests=%d dispatched=%d, want only later job dispatched", calls, len(next.jobs))
				}
				if elapsed := time.Since(start); elapsed > 2*time.Second {
					t.Errorf("later job waited %s, want no wait for the expired batch's Retry-After", elapsed)
				}
			})
		})
	}
}

func TestRetryDoesNotDispatchCanceledExpiredOrNotIssuedJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		expired, expire := context.WithTimeout(ctx, time.Second)
		defer expire()
		calls := 0
		client := clientFunc(func(_ context.Context, ids []string, _ int) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
			calls++
			if calls == 1 {
				return nil, 3 * time.Second, api.AgentError{StatusCode: 429, Message: "throttled"}
			}
			response := &api.IssueJobAcquisitionTokensResponse{NotIssued: []string{"remote-canceled"}}
			for _, id := range ids {
				if id == "canceled" || id == "expired" {
					t.Errorf("retried ineligible job %s", id)
				}
				if id == "eligible" {
					response.JobAcquisitionTokens = append(response.JobAcquisitionTokens, api.IssuedJobAcquisitionToken{JobUUID: id, JobAcquisitionToken: "token"})
				}
			}
			return response, 0, nil
		})
		next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 4)}
		h := New(ctx, slog.Default(), client, next, 0)
		results := make(chan error, 4)
		for id, jobCtx := range map[string]context.Context{"eligible": ctx, "canceled": canceled, "expired": expired, "remote-canceled": ctx} {
			go func() { results <- h.Handle(jobCtx, &api.AgentScheduledJob{ID: id}) }()
		}
		time.Sleep(500 * time.Millisecond)
		cancel()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if len(results) != 4 || len(next.jobs) != 1 {
			t.Fatalf("%d completed calls, %d dispatched jobs; want 4 and 1", len(results), len(next.jobs))
		}
		if job := <-next.jobs; job.ID != "eligible" {
			t.Errorf("dispatched %s, want eligible", job.ID)
		}
		var succeeded, canceledCount, expiredCount, rejected int
		for range 4 {
			switch err := <-results; {
			case err == nil:
				succeeded++
			case errors.Is(err, context.Canceled):
				canceledCount++
			case errors.Is(err, context.DeadlineExceeded):
				expiredCount++
			default:
				rejected++
			}
		}
		if succeeded != 1 || canceledCount != 1 || expiredCount != 1 || rejected != 1 {
			t.Errorf("results: success=%d canceled=%d expired=%d rejected=%d, want one each", succeeded, canceledCount, expiredCount, rejected)
		}
	})
}
