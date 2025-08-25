package stacksapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestListScheduledJobs(t *testing.T) {
	t.Parallel()
	t.Run("single page of results", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "GET", "/stacks/test-stack/scheduled_jobs")

			// Verify query parameters
			expectedQueue := "test-queue"
			if r.URL.Query().Get("queue_key") != expectedQueue {
				t.Errorf("expected queue_key %s, got %s", expectedQueue, r.URL.Query().Get("queue_key"))
			}

			scheduledAt := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
			response := ListScheduledJobsResponse{
				Jobs: []ScheduledJob{
					{
						ID:              "job-1",
						Priority:        5,
						AgentQueryRules: []string{"queue=default"},
						ScheduledAt:     scheduledAt,
					},
					{
						ID:              "job-2",
						Priority:        10,
						AgentQueryRules: []string{"queue=default", "os=linux"},
						ScheduledAt:     scheduledAt.Add(5 * time.Minute),
					},
				},
				ClusterQueue: "test-queue",
				PageInfo: struct {
					HasNextPage bool   `json:"has_next_page"`
					EndCursor   string `json:"end_cursor"`
				}{
					HasNextPage: false,
					EndCursor:   "cursor-end",
				},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()
		params := ListScheduledJobsParams{QueueKey: "test-queue"}

		jobs, nextParams, err := stack.ListScheduledJobs(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(jobs) != 2 {
			t.Errorf("expected 2 jobs, got %d", len(jobs))
		}

		expectedJob1 := ScheduledJob{
			ID:              "job-1",
			Priority:        5,
			AgentQueryRules: []string{"queue=default"},
			ScheduledAt:     time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		}

		if diff := cmp.Diff(expectedJob1, jobs[0]); diff != "" {
			t.Errorf("first job mismatch (-want +got):\n%s", diff)
		}

		// No next page, so nextParams should be nil
		if nextParams != nil {
			t.Errorf("expected nil nextParams for single page, got %+v", nextParams)
		}
	})

	t.Run("multiple pages of results", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "GET", "/stacks/test-stack/scheduled_jobs")

			scheduledAt := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
			response := ListScheduledJobsResponse{
				Jobs: []ScheduledJob{
					{
						ID:              "job-1",
						Priority:        5,
						AgentQueryRules: []string{"queue=default"},
						ScheduledAt:     scheduledAt,
					},
				},
				ClusterQueue: "test-queue",
				PageInfo: struct {
					HasNextPage bool   `json:"has_next_page"`
					EndCursor   string `json:"end_cursor"`
				}{
					HasNextPage: true,
					EndCursor:   "cursor-page1-end",
				},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()
		params := ListScheduledJobsParams{QueueKey: "test-queue"}

		jobs, nextParams, err := stack.ListScheduledJobs(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(jobs) != 1 {
			t.Errorf("expected 1 job, got %d", len(jobs))
		}

		// Should have next page params
		if nextParams == nil {
			t.Fatal("expected non-nil nextParams for multiple pages")
		}

		expectedNextParams := &ListScheduledJobsParams{
			QueueKey:    "test-queue",
			AfterCursor: "cursor-page1-end",
		}

		if diff := cmp.Diff(expectedNextParams, nextParams); diff != "" {
			t.Errorf("next params mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusBadRequest, "Invalid queue")
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()
		params := ListScheduledJobsParams{QueueKey: "invalid-queue"}

		_, _, err := stack.ListScheduledJobs(ctx, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !strings.Contains(err.Error(), "400") {
			t.Errorf("expected error to contain status code, got: %v", err)
		}
	})
}

func TestPaginateAllScheduledJobs(t *testing.T) {
	t.Parallel()
	t.Run("collects all pages", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "GET", "/stacks/test-stack/scheduled_jobs")
			callCount++

			scheduledAt := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
			var response ListScheduledJobsResponse

			if callCount == 1 {
				// First page
				if r.URL.Query().Get("after_cursor") != "" {
					t.Error("first request should not have after_cursor")
				}
				response = ListScheduledJobsResponse{
					Jobs: []ScheduledJob{
						{ID: "job-1", Priority: 5, AgentQueryRules: []string{"queue=default"}, ScheduledAt: scheduledAt},
						{ID: "job-2", Priority: 10, AgentQueryRules: []string{"queue=default"}, ScheduledAt: scheduledAt},
					},
					ClusterQueue: "test-queue",
					PageInfo: struct {
						HasNextPage bool   `json:"has_next_page"`
						EndCursor   string `json:"end_cursor"`
					}{HasNextPage: true, EndCursor: "cursor-page1"},
				}
			} else {
				// Second page
				if r.URL.Query().Get("after_cursor") != "cursor-page1" {
					t.Errorf("second request should have after_cursor=cursor-page1, got %s", r.URL.Query().Get("after_cursor"))
				}
				response = ListScheduledJobsResponse{
					Jobs: []ScheduledJob{
						{ID: "job-3", Priority: 15, AgentQueryRules: []string{"queue=default"}, ScheduledAt: scheduledAt},
					},
					ClusterQueue: "test-queue",
					PageInfo: struct {
						HasNextPage bool   `json:"has_next_page"`
						EndCursor   string `json:"end_cursor"`
					}{HasNextPage: false, EndCursor: "cursor-page2"},
				}
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()
		params := ListScheduledJobsParams{QueueKey: "test-queue"}

		allJobs, err := stack.PaginateAllScheduledJobs(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if callCount != 2 {
			t.Errorf("expected 2 API calls, got %d", callCount)
		}

		if len(allJobs) != 3 {
			t.Errorf("expected 3 jobs total, got %d", len(allJobs))
		}

		// Verify we got jobs from both pages
		jobIDs := make([]string, len(allJobs))
		for i, job := range allJobs {
			jobIDs[i] = job.ID
		}
		expectedIDs := []string{"job-1", "job-2", "job-3"}
		if diff := cmp.Diff(expectedIDs, jobIDs); diff != "" {
			t.Errorf("job IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles error during pagination", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				// First call succeeds
				scheduledAt := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
				response := ListScheduledJobsResponse{
					Jobs: []ScheduledJob{
						{ID: "job-1", Priority: 5, AgentQueryRules: []string{"queue=default"}, ScheduledAt: scheduledAt},
					},
					ClusterQueue: "test-queue",
					PageInfo: struct {
						HasNextPage bool   `json:"has_next_page"`
						EndCursor   string `json:"end_cursor"`
					}{HasNextPage: true, EndCursor: "cursor-page1"},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			} else {
				// Second call fails
				respondWithError(w, http.StatusInternalServerError, "Server error")
			}
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()
		params := ListScheduledJobsParams{QueueKey: "test-queue"}

		_, err := stack.PaginateAllScheduledJobs(ctx, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// First call succeeds, then retries happen on the 500 error (5 attempts by default)
		if callCount != 6 {
			t.Errorf("expected 6 API calls (1 success + 5 retries), got %d", callCount)
		}
	})
}
