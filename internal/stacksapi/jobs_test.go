package stacksapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGetJob(t *testing.T) {
	t.Parallel()
	t.Run("successful get", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "GET", "/stacks/test-stack/jobs/job-123")

			response := Job{
				ID:      "job-123",
				Command: "echo hello",
				Env:     map[string]string{"FOO": "bar", "TEST": "value"},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		job, err := stack.GetJob(ctx, "job-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := Job{
			ID:      "job-123",
			Command: "echo hello",
			Env:     map[string]string{"FOO": "bar", "TEST": "value"},
		}

		if diff := cmp.Diff(expected, job); diff != "" {
			t.Errorf("job mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusNotFound, "Job not found")
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		_, err := stack.GetJob(ctx, "nonexistent-job")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !strings.Contains(err.Error(), "404") {
			t.Errorf("expected error to contain status code, got: %v", err)
		}
	})
}

func TestGetJobState(t *testing.T) {
	t.Parallel()
	t.Run("successful get", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "GET", "/stacks/test-stack/jobs/job-123/state")

			response := JobState{
				ID:    "job-123",
				State: StackStateConnected,
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		state, err := stack.GetJobState(ctx, "job-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := JobState{
			ID:    "job-123",
			State: StackStateConnected,
		}

		if diff := cmp.Diff(expected, state); diff != "" {
			t.Errorf("job state mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusNotFound, "Job not found")
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		_, err := stack.GetJobState(ctx, "nonexistent-job")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestFailJob(t *testing.T) {
	t.Parallel()
	t.Run("successful fail", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "POST", "/stacks/test-stack/jobs/job-123/fail")

			// Verify request body
			var params FailJobParams
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}

			expectedExitStatus := 1
			expected := FailJobParams{
				Reason:     "Infrastructure provisioning failed",
				ExitStatus: &expectedExitStatus,
			}

			if diff := cmp.Diff(expected, params); diff != "" {
				t.Errorf("fail job params mismatch (-want +got):\n%s", diff)
			}

			w.WriteHeader(http.StatusOK)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		exitStatus := 1
		params := FailJobParams{
			Reason:     "Infrastructure provisioning failed",
			ExitStatus: &exitStatus,
		}

		err := stack.FailJob(ctx, "job-123", params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusBadRequest, "Invalid job state")
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		params := FailJobParams{Reason: "test failure"}
		err := stack.FailJob(ctx, "job-123", params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !strings.Contains(err.Error(), "400") {
			t.Errorf("expected error to contain status code, got: %v", err)
		}
	})
}

func TestReserveJobs(t *testing.T) {
	t.Parallel()
	t.Run("successful reservation", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "POST", "/stacks/test-stack/jobs/reserve")

			// Verify request body
			var params ReserveJobsParams
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}

			expirySeconds := 300
			expected := ReserveJobsParams{
				JobIDs:                   []string{"job-1", "job-2", "job-3"},
				ReservationExpirySeconds: &expirySeconds,
			}

			if diff := cmp.Diff(expected, params); diff != "" {
				t.Errorf("reserve jobs params mismatch (-want +got):\n%s", diff)
			}

			response := JobReservations{
				Reserved:    []string{"job-1", "job-2"},
				NotReserved: []string{"job-3"},
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		expirySeconds := 300
		params := ReserveJobsParams{
			JobIDs:                   []string{"job-1", "job-2", "job-3"},
			ReservationExpirySeconds: &expirySeconds,
		}

		reservations, err := stack.ReserveJobs(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := JobReservations{
			Reserved:    []string{"job-1", "job-2"},
			NotReserved: []string{"job-3"},
		}

		if diff := cmp.Diff(expected, reservations); diff != "" {
			t.Errorf("reservations mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusInternalServerError, "Service unavailable")
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}
		ctx := context.Background()

		params := ReserveJobsParams{JobIDs: []string{"job-1"}}
		_, err := stack.ReserveJobs(ctx, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
