package stacksapi

import (
	"encoding/json"
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
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/scheduled-jobs")

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
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/scheduled-jobs")
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

func TestBatchReserveJobs(t *testing.T) {
	t.Parallel()

	t.Run("successful batch reserve jobs", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "PUT", "/stacks/stack-123/scheduled-jobs/batch-reserve")

			var params BatchReserveJobsRequest
			err := json.NewDecoder(r.Body).Decode(&params)
			if err != nil {
				t.Fatalf("failed to decode batch reserve request body: %v", err)
			}

			expectedParams := BatchReserveJobsRequest{
				JobUUIDs:                 []string{"job-1", "job-2", "job-3"},
				ReservationExpirySeconds: 600,
			}

			if diff := cmp.Diff(expectedParams, params); diff != "" {
				t.Errorf("request params mismatch (-want +got):\n%s", diff)
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BatchReserveJobsResponse{
				Reserved:    []string{"job-1", "job-3"},
				NotReserved: []string{"job-2"},
			})
			w.WriteHeader(http.StatusOK)
		})
		t.Cleanup(server.Close)

		req := BatchReserveJobsRequest{
			StackKey:                 "stack-123",
			JobUUIDs:                 []string{"job-1", "job-2", "job-3"},
			ReservationExpirySeconds: 600,
		}

		resp, _, err := client.BatchReserveJobs(t.Context(), req)
		if err != nil {
			t.Fatalf("client.BatchReserveJobs error = %v, want nil", err)
		}
		expectedResp := &BatchReserveJobsResponse{
			Reserved:    []string{"job-1", "job-3"},
			NotReserved: []string{"job-2"},
		}

		if diff := cmp.Diff(expectedResp, resp); diff != "" {
			t.Errorf("batch reserve jobs response mismatch (-want +got):\n%s", diff)
		}
	})
}
