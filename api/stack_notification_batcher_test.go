package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/buildkite/stacksapi"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestNotificationBatcher_SendsBatchesPeriodically(t *testing.T) {
	t.Parallel()

	server := NewFakeAgentServer()
	defer server.Close()

	serverURL, _ := url.Parse(server.URL())
	client, err := stacksapi.NewClient("fake-token", stacksapi.WithBaseURL(serverURL))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	nb := newNotificationBatcher("test-stack", client, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := nb.start(ctx); err != nil {
		t.Fatalf("start() error = %v", err)
	}

	// Add notifications
	want := []stacksapi.StackNotification{
		{JobUUID: "job-A", Detail: "detail A"},
		{JobUUID: "job-B", Detail: "detail B"},
		{JobUUID: "job-C", Detail: "detail C"},
	}
	for _, n := range want {
		if err := nb.add(ctx, n); err != nil {
			t.Fatalf("add() error = %v", err)
		}
	}

	// Wait for ticker to flush (interval is 100ms)
	time.Sleep(250 * time.Millisecond)

	if got, wantLen := len(server.NotificationCalls), 1; got != wantLen {
		t.Fatalf("number of notification calls = %d, want %d", got, wantLen)
	}

	if diff := cmp.Diff(want, server.NotificationCalls[0], cmpopts.IgnoreFields(stacksapi.StackNotification{}, "Timestamp")); diff != "" {
		t.Errorf("notifications mismatch (-want +got):\n%s", diff)
	}

	cancel()
	nb.waitDone()
}

func TestNotificationBatcher_ChunksBatchesBySize(t *testing.T) {
	t.Parallel()

	server := NewFakeAgentServer()
	defer server.Close()

	serverURL, _ := url.Parse(server.URL())
	client, err := stacksapi.NewClient("fake-token", stacksapi.WithBaseURL(serverURL))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	nb := newNotificationBatcher("test-stack", client, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := nb.start(ctx); err != nil {
		t.Fatalf("start() error = %v", err)
	}

	// Add more than maxNotificationsPerBatch (1000) notifications
	for i := range 2500 {
		note := stacksapi.StackNotification{
			JobUUID: fmt.Sprintf("job-%d", i),
			Detail:  "detail",
		}
		if err := nb.add(ctx, note); err != nil {
			t.Fatalf("i=%d add(%v) error = %v", i, note, err)
		}
	}

	// Wait for ticker to flush (interval is 100ms)
	time.Sleep(250 * time.Millisecond)

	// Should be chunked into 3 calls: 1000 + 1000 + 500
	if got, want := len(server.NotificationCalls), 3; got != want {
		t.Fatalf("number of notification calls = %d, want %d", got, want)
	}

	// Each batch should be at most 1000
	for i, batch := range server.NotificationCalls {
		if len(batch) > 1000 {
			t.Errorf("batch %d size = %d, want <= 1000", i, len(batch))
		}
	}

	// Total should be 2500
	total := 0
	for _, batch := range server.NotificationCalls {
		total += len(batch)
	}
	if got, want := total, 2500; got != want {
		t.Errorf("total notifications = %d, want %d", got, want)
	}

	cancel()
	nb.waitDone()
}
