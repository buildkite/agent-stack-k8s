package stacksapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestFinishJob(t *testing.T) {
	t.Parallel()

	t.Run("successful finish job", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "POST", "/stacks/stack-123/jobs/456/finish")

			var params FinishJobRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			expectedParams := FinishJobRequest{
				ExitStatus: -10,
				Detail:     "show me the money",
			}

			if diff := cmp.Diff(expectedParams, params); diff != "" {
				t.Errorf("request params mismatch (-want +got):\n%s", diff)
			}

			w.Header().Set("X-Custom-Header", "custom-value")
			w.WriteHeader(http.StatusNoContent)
		})
		t.Cleanup(func() { server.Close() })

		req := FinishJobRequest{
			StackKey:   "stack-123",
			JobUUID:    "456",
			ExitStatus: -10,
			Detail:     "show me the money",
		}

		header, err := client.FinishJob(t.Context(), req)
		if err != nil {
			t.Fatalf("client.FinishJob returned an error: %v", err)
		}

		want, got := "custom-value", header.Get("X-Custom-Header")
		if want != got {
			t.Errorf("header X-Custom-Header: want %q, got %q", want, got)
		}
	})

	t.Run("crops detail when exceeds 4KB", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "POST", "/stacks/stack-123/jobs/456/finish")

			var params FinishJobRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			assert.Equal(t, 4*1024, len(params.Detail))

			w.WriteHeader(http.StatusNoContent)
		})
		t.Cleanup(func() { server.Close() })

		largeDetail := strings.Repeat("x", 5*1024)

		req := FinishJobRequest{
			StackKey:   "stack-123",
			JobUUID:    "456",
			ExitStatus: -10,
			Detail:     largeDetail,
		}

		_, err := client.FinishJob(t.Context(), req)
		assert.NoError(t, err)
	})
}
