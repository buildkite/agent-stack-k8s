package stacksapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestFailJob(t *testing.T) {
	t.Parallel()

	t.Run("successful fail job", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "POST", "/stacks/stack-123/jobs/456/fail")

			var params FailJobRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			expectedParams := FailJobRequest{
				ExitStatus:  -10,
				ErrorDetail: "show me the money",
			}

			if diff := cmp.Diff(expectedParams, params); diff != "" {
				t.Errorf("request params mismatch (-want +got):\n%s", diff)
			}

			w.WriteHeader(http.StatusNoContent)
		})
		t.Cleanup(func() { server.Close() })

		req := FailJobRequest{
			StackKey:    "stack-123",
			JobUUID:     "456",
			ExitStatus:  -10,
			ErrorDetail: "show me the money",
		}

		err := client.FailJob(t.Context(), req)
		assert.NoError(t, err)
	})

	t.Run("crops error detail when exceeds 4KB", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "POST", "/stacks/stack-123/jobs/456/fail")

			var params FailJobRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			// Verify the error detail was cropped to 4KB
			assert.Equal(t, 4*1024, len(params.ErrorDetail))

			w.WriteHeader(http.StatusNoContent)
		})
		t.Cleanup(func() { server.Close() })

		// Create an error detail larger than 4KB
		largeErrorDetail := strings.Repeat("x", 5*1024) // 5KB

		req := FailJobRequest{
			StackKey:    "stack-123",
			JobUUID:     "456",
			ExitStatus:  -10,
			ErrorDetail: largeErrorDetail,
		}

		err := client.FailJob(t.Context(), req)
		assert.NoError(t, err)
	})
}
