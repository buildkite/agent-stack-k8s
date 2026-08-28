package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/stacksapi"
	"github.com/google/go-cmp/cmp"
)

func TestFakeAgentServer_IssuesJobAcquisitionTokens(t *testing.T) {
	ctx := t.Context()
	server := api.NewFakeAgentServer()
	defer server.Close()

	expiresAt := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	server.JobAcquisitionTokenResponse = &api.IssueJobAcquisitionTokensResponse{
		JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{
			JobUUID:             "job-1",
			JobAcquisitionToken: "jat-1",
			ExpiresAt:           expiresAt,
		}},
		NotIssued: []string{"job-2"},
	}

	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: server.URL(),
		StackID:  "test-stack",
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	got, _, err := client.IssueJobAcquisitionTokens(ctx, []string{"job-1", "job-2"}, 1800)
	if err != nil {
		t.Fatalf("IssueJobAcquisitionTokens() error = %v", err)
	}
	if diff := cmp.Diff(got, server.JobAcquisitionTokenResponse); diff != "" {
		t.Errorf("IssueJobAcquisitionTokens() diff (-got +want):\n%s", diff)
	}
	wantCalls := []api.IssueJobAcquisitionTokensRequest{{JobUUIDs: []string{"job-1", "job-2"}, TokenLifetimeSeconds: new(1800)}}
	if diff := cmp.Diff(server.JobAcquisitionTokenCalls, wantCalls); diff != "" {
		t.Errorf("server.JobAcquisitionTokenCalls diff (-got +want):\n%s", diff)
	}
	if got := server.JobAcquisitionTokenStatusCode; got != http.StatusCreated {
		t.Errorf("JobAcquisitionTokenStatusCode = %d, want %d", got, http.StatusCreated)
	}
}

func TestIssueJobAcquisitionTokensOmitsDefaultTokenLifetime(t *testing.T) {
	server := api.NewFakeAgentServer()
	defer server.Close()
	client, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
		Token: "fake-token", Endpoint: server.URL(), StackID: "test-stack", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	if _, _, err := client.IssueJobAcquisitionTokens(t.Context(), []string{"job-1"}, 0); err != nil {
		t.Fatalf("IssueJobAcquisitionTokens() error = %v", err)
	}
	if bytes.Contains(server.JobAcquisitionTokenRequestBodies[0], []byte("token_lifetime_seconds")) {
		t.Errorf("request body contains token_lifetime_seconds: %s", server.JobAcquisitionTokenRequestBodies[0])
	}
}

func TestIssueJobAcquisitionTokensDoesNotLogTokenPayload(t *testing.T) {
	ctx := t.Context()
	server := api.NewFakeAgentServer()
	defer server.Close()
	server.JobAcquisitionTokenResponse = &api.IssueJobAcquisitionTokensResponse{
		JobAcquisitionTokens: []api.IssuedJobAcquisitionToken{{
			JobUUID:             "job-1",
			JobAcquisitionToken: "jat-secret",
		}},
	}

	var logs bytes.Buffer
	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:           "cluster-secret",
		Endpoint:        server.URL(),
		StackID:         "test-stack",
		Logger:          slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		LogHTTPPayloads: true,
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	if _, _, err := client.IssueJobAcquisitionTokens(ctx, []string{"job-1"}, 0); err != nil {
		t.Fatalf("IssueJobAcquisitionTokens() error = %v", err)
	}
	if strings.Contains(logs.String(), "jat-secret") {
		t.Errorf("HTTP logs contain JAT: %s", logs.String())
	}
	if strings.Contains(logs.String(), "cluster-secret") {
		t.Errorf("HTTP logs contain cluster token: %s", logs.String())
	}
}

func TestIssueJobAcquisitionTokensRejectsMoreThan1000Jobs(t *testing.T) {
	ctx := t.Context()
	server := api.NewFakeAgentServer()
	defer server.Close()
	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token: "fake-token", Endpoint: server.URL(), StackID: "test-stack", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	if _, _, err := client.IssueJobAcquisitionTokens(ctx, make([]string, 1001), 0); err == nil {
		t.Fatal("IssueJobAcquisitionTokens() error = nil, want non-nil")
	}
	if got := len(server.JobAcquisitionTokenCalls); got != 0 {
		t.Errorf("issuance calls = %d, want 0", got)
	}
}

func TestIssueJobAcquisitionTokensRejectsInvalidTokenLifetime(t *testing.T) {
	server := api.NewFakeAgentServer()
	defer server.Close()
	client, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
		Token: "fake-token", Endpoint: server.URL(), StackID: "test-stack", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	for _, lifetime := range []int{-1, 3601} {
		if _, _, err := client.IssueJobAcquisitionTokens(t.Context(), []string{"job-1"}, lifetime); err == nil {
			t.Errorf("IssueJobAcquisitionTokens(tokenLifetimeSeconds: %d) error = nil, want non-nil", lifetime)
		}
	}
	if got := len(server.JobAcquisitionTokenCalls); got != 0 {
		t.Errorf("issuance calls = %d, want 0", got)
	}
}

func TestAgentScheduledJobDoesNotSerializeJobAcquisitionToken(t *testing.T) {
	b, err := json.Marshal(api.AgentScheduledJob{ID: "job-1", JobAcquisitionToken: "jat-secret"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(b), "jat-secret") {
		t.Errorf("serialized AgentScheduledJob contains JAT: %s", b)
	}
}

func TestFakeAgentServer_DefaultBehavior(t *testing.T) {
	ctx := t.Context()

	// Create fake server
	server := api.NewFakeAgentServer()
	defer server.Close()

	// Create REAL AgentClient pointing to fake server
	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: server.URL(),
		StackID:  "test-stack",
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	jobIDs := []string{"job-1", "job-2", "job-3"}
	result, retryAfter, err := client.ReserveJobs(ctx, jobIDs, 0)
	if err != nil {
		t.Errorf("ReserveJobs(ctx, %q) error = %v, want nil", jobIDs, err)
	}
	if retryAfter != 0 {
		t.Errorf("ReserveJobs(ctx, %q) retryAfter = %v, want 0", jobIDs, retryAfter)
	}

	want := &stacksapi.BatchReserveJobsResponse{
		Reserved:    jobIDs,
		NotReserved: []string{},
	}
	if diff := cmp.Diff(result, want); diff != "" {
		t.Errorf("ReserveJobs(ctx, %q) diff (-got +want):\n%s", jobIDs, diff)
	}
}

func TestFakeAgentServer_RecordsCalls(t *testing.T) {
	ctx := t.Context()

	server := api.NewFakeAgentServer()
	defer server.Close()

	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: server.URL(),
		StackID:  "test-stack",
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	jobIDs := []string{"job-1", "job-2"}
	_, _, err = client.ReserveJobs(ctx, jobIDs, 0)
	if err != nil {
		t.Fatalf("ReserveJobs(ctx, %q) error = %v", jobIDs, err)
	}
	_, _, err = client.ReserveJobs(ctx, []string{"job-3"}, 0)
	if err != nil {
		t.Fatalf("ReserveJobs(ctx, %q) error = %v", jobIDs, err)
	}

	want := [][]string{
		{"job-1", "job-2"},
		{"job-3"},
	}
	if diff := cmp.Diff(server.ReserveCalls, want); diff != "" {
		t.Errorf("server.ReserveCalls diff (-got +want):\n%s", diff)
	}
}

func TestFakeAgentServer_CustomError(t *testing.T) {
	ctx := t.Context()

	server := api.NewFakeAgentServer()
	defer server.Close()

	server.ReserveError = "reservation failed"
	server.ReserveStatusCode = 500

	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: server.URL(),
		StackID:  "test-stack",
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	jobIDs := []string{"job-1"}
	result, _, err := client.ReserveJobs(ctx, []string{"job-1"}, 0)
	if err == nil {
		t.Errorf("ReserveJobs(ctx, %q) error = nil, want error", jobIDs)
	}
	if result != nil {
		t.Errorf("ReserveJobs(ctx, %q) result = %v, want nil", jobIDs, result)
	}
}

func TestFakeAgentServer_CustomResult(t *testing.T) {
	ctx := t.Context()

	server := api.NewFakeAgentServer()
	defer server.Close()

	want := &stacksapi.BatchReserveJobsResponse{
		Reserved:    []string{"job-1"},
		NotReserved: []string{"job-2", "job-3"},
	}
	server.ReserveResponse = want

	client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: server.URL(),
		StackID:  "test-stack",
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}

	jobIDs := []string{"job-1", "job-2", "job-3"}
	result, _, err := client.ReserveJobs(ctx, jobIDs, 0)
	if err != nil {
		t.Errorf("ReserveJobs(ctx, %q) error = %v, want nil", jobIDs, err)
	}

	if diff := cmp.Diff(result, want); diff != "" {
		t.Errorf("ReserveJobs(ctx, %q) diff (-got +want):\n%s", jobIDs, diff)
	}
}
