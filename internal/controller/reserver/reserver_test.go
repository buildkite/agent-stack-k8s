package reserver

import (
	"context"
	"log/slog"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"

	"github.com/google/uuid"
)

type fakeHandler struct {
	called int
}

func (f *fakeHandler) HandleMany(ctx context.Context, jobs []*api.AgentScheduledJob) error {
	f.called++
	return nil
}

func (f *fakeHandler) Pause(pause bool) {}

func TestReserver_ChunksJobsInto1000(t *testing.T) {
	t.Parallel()

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

	fakeNext := &fakeHandler{}
	r := New(slog.Default(), client, fakeNext)

	var jobs []*api.AgentScheduledJob
	for range 2500 {
		id := uuid.New().String()
		jobs = append(jobs, &api.AgentScheduledJob{ID: id})
	}

	if err := r.HandleMany(ctx, jobs); err != nil {
		t.Errorf("r.HandleMany(ctx, jobs) = %v", err)
	}

	if got, want := len(server.ReserveCalls), 3; got != want {
		t.Fatalf("number of ReserveJobs calls = %d, want %d", got, want)
	}

	if got, want := len(server.ReserveCalls[0]), 1000; got != want {
		t.Errorf("first chunk size = %d, want %d", got, want)
	}
	if got, want := len(server.ReserveCalls[1]), 1000; got != want {
		t.Errorf("second chunk size = %d, want %d", got, want)
	}
	if got, want := len(server.ReserveCalls[2]), 500; got != want {
		t.Errorf("third chunk size = %d, want %d", got, want)
	}

	if got, want := fakeNext.called, 1; got != want {
		t.Errorf("next handler called = %d times, want %d", got, want)
	}
}
