package stacksapi

import (
	"encoding/json"
	"net/http"
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
}
