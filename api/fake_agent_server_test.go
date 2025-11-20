package api_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/stacksapi"
	"github.com/google/go-cmp/cmp"
)

func TestFakeAgentServer_DefaultBehavior(t *testing.T) {
	ctx := context.Background()

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
	result, retryAfter, err := client.ReserveJobs(ctx, jobIDs)
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
	ctx := context.Background()

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
	_, _, err = client.ReserveJobs(ctx, jobIDs)
	if err != nil {
		t.Fatalf("ReserveJobs(ctx, %q) error = %v", jobIDs, err)
	}
	_, _, err = client.ReserveJobs(ctx, []string{"job-3"})
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
	ctx := context.Background()

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
	result, _, err := client.ReserveJobs(ctx, []string{"job-1"})
	if err == nil {
		t.Errorf("ReserveJobs(ctx, %q) error = nil, want error", jobIDs)
	}
	if result != nil {
		t.Errorf("ReserveJobs(ctx, %q) result = %v, want nil", jobIDs, result)
	}
}

func TestFakeAgentServer_CustomResult(t *testing.T) {
	ctx := context.Background()

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
	result, _, err := client.ReserveJobs(ctx, jobIDs)
	if err != nil {
		t.Errorf("ReserveJobs(ctx, %q) error = %v, want nil", jobIDs, err)
	}

	if diff := cmp.Diff(result, want); diff != "" {
		t.Errorf("ReserveJobs(ctx, %q) diff (-got +want):\n%s", jobIDs, diff)
	}
}
