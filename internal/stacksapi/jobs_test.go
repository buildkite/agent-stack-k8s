package stacksapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var listResp = &ListScheduledJobsResponse{
	Jobs: []ScheduledJob{
		{ID: "job-1"},
		{ID: "job-2"},
	},
	ClusterQueue: ClusterQueue{ID: "queue-456", Paused: false},
	PageInfo:     PageInfo{HasNextPage: false, EndCursor: ""},
}

func TestListScheduledJobs(t *testing.T) {
	t.Parallel()

	t.Run("encodes filled query params correctly", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/scheduled_jobs")

			q := r.URL.Query()
			gotKey, wantKey := q.Get("queue_key"), "queue-456"
			if gotKey != wantKey {
				t.Errorf("queue_key = %q, want %q", gotKey, wantKey)
			}

			gotLimit, wantLimit := q.Get("limit"), "50"
			if gotLimit != wantLimit {
				t.Errorf("limit = %q, want %q", gotLimit, wantLimit)
			}

			gotAfter, wantAfter := q.Get("after"), "cursor-789"
			if gotAfter != wantAfter {
				t.Errorf("after = %q, want %q", gotAfter, wantAfter)
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(listResp)
			w.WriteHeader(http.StatusOK)
		})
		t.Cleanup(server.Close)

		req := ListScheduledJobsRequest{
			StackKey:        "stack-123",
			ClusterQueueKey: "queue-456",
			PageSize:        50,
			StartCursor:     "cursor-789",
		}

		jobs, _, err := client.ListScheduledJobs(t.Context(), req)
		if err != nil {
			t.Fatalf("client.ListScheduledJobs error = %v, expected nil", err)
		}

		if diff := cmp.Diff(listResp, jobs); diff != "" {
			t.Errorf("list scheduled jobs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("encodes missing optional query params correctly", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/scheduled_jobs")
			q := r.URL.Query()

			gotKey, wantKey := q.Get("queue_key"), "queue-456"
			if gotKey != wantKey {
				t.Errorf("queue_key = %q, want %q", gotKey, wantKey)
			}

			if limit := q.Get("limit"); limit != "" {
				t.Errorf("limit = %q, want empty", limit)
			}

			if after := q.Get("after"); after != "" {
				t.Errorf("after = %q, want empty", after)
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(listResp)
		})
		t.Cleanup(server.Close)

		req := ListScheduledJobsRequest{
			StackKey:        "stack-123",
			ClusterQueueKey: "queue-456",
		}

		jobs, _, err := client.ListScheduledJobs(t.Context(), req)
		if err != nil {
			t.Fatalf("client.ListScheduledJobs error = %v, expected nil", err)
		}

		if diff := cmp.Diff(listResp, jobs); diff != "" {
			t.Errorf("list scheduled jobs mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestPaginateAllScheduledJobs(t *testing.T) {
	t.Parallel()

	t.Run("paginates through multiple pages", func(t *testing.T) {
		t.Parallel()

		allJobs := []ScheduledJob{
			{ID: "job-1"},
			{ID: "job-2"},
			{ID: "job-3"},
			{ID: "job-4"},
			{ID: "job-5"},
			{ID: "job-6"},
		}

		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/scheduled_jobs")

			q := r.URL.Query()
			gotKey, wantKey := q.Get("queue_key"), "queue-456"
			if gotKey != wantKey {
				t.Errorf("queue_key = %q, want %q", gotKey, wantKey)
			}

			resp := ListScheduledJobsResponse{
				ClusterQueue: ClusterQueue{ID: "queue-456", Paused: false},
				Jobs:         allJobs[callCount*2 : (callCount*2)+2], // 2 jobs per page
				PageInfo: PageInfo{
					HasNextPage: callCount < 2,
					EndCursor:   fmt.Sprintf("cursor-%d", callCount),
				},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			callCount++
		})
		t.Cleanup(server.Close)

		req := ListScheduledJobsRequest{
			StackKey:        "stack-123",
			ClusterQueueKey: "queue-456",
		}

		jobs, _, err := client.PaginateAllScheduledJobs(t.Context(), req)
		if err != nil {
			t.Fatalf("client.PaginateAllScheduledJobs error = %v, expected nil", err)
		}

		if cmp.Diff(jobs.Jobs, allJobs) != "" {
			t.Errorf("all jobs mismatch (-want +got):\n%s", cmp.Diff(jobs.Jobs, allJobs))
		}
	})
}
