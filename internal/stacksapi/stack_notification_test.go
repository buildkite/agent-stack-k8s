package stacksapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestCreateStackNotification(t *testing.T) {
	t.Parallel()

	t.Run("successful create stack notification", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "POST", "/stacks/stack-123/jobs/456/stack_notifications")

			var params CreateStackNotificationRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			expectedParams := CreateStackNotificationRequest{
				Detail: "Pod is starting up",
			}

			if diff := cmp.Diff(expectedParams, params); diff != "" {
				t.Errorf("request params mismatch (-want +got):\n%s", diff)
			}

			w.Header().Set("X-Custom-Header", "custom-value")
			w.WriteHeader(http.StatusOK)
		})
		t.Cleanup(func() { server.Close() })

		req := CreateStackNotificationRequest{
			StackKey: "stack-123",
			JobUUID:  "456",
			Detail:   "Pod is starting up",
		}

		header, err := client.CreateStackNotification(t.Context(), req)
		if err != nil {
			t.Fatalf("client.CreateStackNotification returned an error: %v", err)
		}

		want, got := "custom-value", header.Get("X-Custom-Header")
		if want != got {
			t.Errorf("header.Get(\"X-Custom-Header\") = %q, want %q", got, want)
		}
	})

	t.Run("crops detail when exceeds 255 characters", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "POST", "/stacks/stack-123/jobs/456/stack_notifications")

			var params CreateStackNotificationRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			// Verify the detail was cropped to 255 characters
			assert.Equal(t, 255, len(params.Detail))
			assert.True(t, strings.HasSuffix(params.Detail, "… (cropped)"))

			w.WriteHeader(http.StatusOK)
		})
		t.Cleanup(func() { server.Close() })

		// Create a detail longer than 255 characters
		largeDetail := strings.Repeat("x", 300) // 300 characters

		req := CreateStackNotificationRequest{
			StackKey: "stack-123",
			JobUUID:  "456",
			Detail:   largeDetail,
		}

		_, err := client.CreateStackNotification(t.Context(), req)
		assert.NoError(t, err)
	})
}
