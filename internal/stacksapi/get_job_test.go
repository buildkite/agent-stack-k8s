package stacksapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGetJob(t *testing.T) {
	t.Parallel()

	t.Run("successful get job", func(t *testing.T) {
		t.Parallel()

		expectedResponse := GetJobResponse{
			ID:      "job-uuid-123",
			Command: "echo Hello 👋",
			Env: map[string]string{
				"BUILDKITE_JOB_ID": "job-uuid-123",
				"BUILDKITE_BRANCH": "main",
			},
		}

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/jobs/job-uuid-123")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			if err := json.NewEncoder(w).Encode(expectedResponse); err != nil {
				t.Fatalf("failed to encode response: %v", err)
			}
		})
		t.Cleanup(func() { server.Close() })

		req := GetJobRequest{
			StackKey: "stack-123",
			JobUUID:  "job-uuid-123",
		}

		resp, _, err := client.GetJob(t.Context(), req)
		if err != nil {
			t.Fatalf("client.GetJob returned an error: %v", err)
		}

		if diff := cmp.Diff(expectedResponse, *resp); diff != "" {
			t.Errorf("response mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("job not found", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "GET", "/stacks/stack-123/jobs/non-existent-uuid")

			w.WriteHeader(http.StatusNotFound)
		})
		t.Cleanup(func() { server.Close() })

		req := GetJobRequest{
			StackKey: "stack-123",
			JobUUID:  "non-existent-uuid",
		}

		_, _, err := client.GetJob(t.Context(), req)
		if err == nil {
			t.Fatal("expected error for not found job, got nil")
		}

		// Check the error response
		var errResp *ErrorResponse
		ok := errors.As(err, &errResp)
		if !ok {
			t.Fatalf("expected ErrorResponse, got: %v", err)
		}

		if errResp.Response.StatusCode != http.StatusNotFound {
			t.Errorf("expected status code %d, got %d", http.StatusNotFound, errResp.Response.StatusCode)
		}
	})
}
