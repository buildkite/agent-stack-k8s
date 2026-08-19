package jatissuer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
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
	calls     [][]string
}

func (f *fakeClient) IssueJobAcquisitionTokens(_ context.Context, ids []string) (*api.IssueJobAcquisitionTokensResponse, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ids)
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
			h := New(slog.Default(), &fakeClient{responses: []*api.IssueJobAcquisitionTokensResponse{test.resp}}, next)
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

func TestHandleRetriesAndForwardsTokenThroughDeduper(t *testing.T) {
	t.Parallel()

	jobID := uuid.NewString()
	client := &fakeClient{
		responses: []*api.IssueJobAcquisitionTokensResponse{nil, {
			JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: jobID, JobAcquisitionToken: "jat-secret"}},
		}},
		errors: []error{errors.New("temporary failure"), nil},
	}
	next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 1)}
	h := New(slog.Default(), client, deduper.New(slog.Default(), next))

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
}

func TestIssuerRunsOnlyAfterLimiterReleasesCapacity(t *testing.T) {
	job1 := uuid.NewString()
	job2 := uuid.NewString()
	client := &fakeClient{responses: []*api.IssueJobAcquisitionTokensResponse{
		{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: job1, JobAcquisitionToken: "jat-1"}}},
		{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: job2, JobAcquisitionToken: "jat-2"}}},
	}}
	next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 2)}
	h := New(slog.Default(), client, next)
	ctx, cancel := context.WithCancel(t.Context())
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
