package jatissuer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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
	const secret = "jat-secret-must-not-appear-in-logs"
	tests := []struct {
		name string
		resp *api.IssueJobAcquisitionTokensResponse
		err  error
	}{
		{name: "API error", err: api.AgentError{Message: "stack queue mismatch", StatusCode: 403}},
		{name: "nil response"},
		{name: "not issued", resp: &api.IssueJobAcquisitionTokensResponse{NotIssued: []string{jobID}}},
		{name: "missing", resp: &api.IssueJobAcquisitionTokensResponse{}},
		{name: "mismatched", resp: &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: uuid.NewString(), JobAcquisitionToken: secret}}}},
		{name: "duplicate", resp: &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: jobID, JobAcquisitionToken: secret}, {JobUUID: jobID, JobAcquisitionToken: secret}}}},
		{name: "empty token", resp: &api.IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{JobUUID: jobID}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := &captureHandler{jobs: make(chan *api.AgentScheduledJob, 1)}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil)) // Default Info level, as in production.
			h := New(logger, &fakeClient{responses: []*api.IssueJobAcquisitionTokensResponse{test.resp}, errors: []error{test.err}}, next, 0)
			if err := h.Handle(t.Context(), &api.AgentScheduledJob{ID: jobID}); err == nil {
				t.Fatal("Handle() error = nil, want non-nil")
			}
			select {
			case <-next.jobs:
				t.Fatal("next handler called for malformed issuance response")
			default:
			}
			var record map[string]any
			// Unmarshal also rejects multiple records: terminal issuance failures
			// should emit exactly one warning after retries finish.
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatalf("expected one visible log record: %v", err)
			}
			if record["level"] != "WARN" || record["job-uuid"] != jobID {
				t.Errorf("log record = %v, want warning for job %s", record, jobID)
			}
			if test.err != nil && record["error"] != test.err.Error() {
				t.Errorf("logged error = %v, want %v", record["error"], test.err)
			}
			if strings.Contains(logs.String(), secret) {
				t.Error("log contains a job acquisition token")
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
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := New(logger, client, deduper.New(logger, next), 1800)

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
	if logs.Len() != 0 {
		t.Errorf("successful retry emitted a log at default level: %s", logs.String())
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
	h := New(slog.Default(), client, next, 0)
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
