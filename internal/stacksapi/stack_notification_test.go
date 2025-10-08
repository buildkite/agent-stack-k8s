package stacksapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestCreateStackNotifications(t *testing.T) {
	t.Parallel()

	t.Run("successful create stack notifications", func(t *testing.T) {
		t.Parallel()

		sampleTime := time.Now()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "POST", "/stacks/stack-123/notifications")

			var params CreateStackNotificationsRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))

			expectedParams := CreateStackNotificationsRequest{
				Notifications: []StackNotification{
					{
						JobUUID:   "456",
						Detail:    "Pod 1 starting",
						Timestamp: sampleTime,
					},
					{
						JobUUID: "789",
						Detail:  "Pod 2 starting",
					},
				},
			}

			if diff := cmp.Diff(expectedParams, params); diff != "" {
				t.Errorf("request params mismatch (-want +got):\n%s", diff)
			}

			w.Header().Set("X-Custom-Header", "custom-value")
			w.WriteHeader(http.StatusOK)
			assert.NoError(t, json.NewEncoder(w).Encode(CreateStackNotificationsResponse{
				Errors: []StackNotificationError{},
			}))
		})
		t.Cleanup(func() { server.Close() })

		req := CreateStackNotificationsRequest{
			StackKey: "stack-123",
			Notifications: []StackNotification{
				{
					JobUUID:   "456",
					Detail:    "Pod 1 starting",
					Timestamp: sampleTime,
				},
				{
					JobUUID: "789",
					Detail:  "Pod 2 starting",
				},
			},
		}

		resp, header, err := client.CreateStackNotifications(t.Context(), req)
		if err != nil {
			t.Fatalf("client.CreateStackNotifications returned an error: %v", err)
		}

		want, got := "custom-value", header.Get("X-Custom-Header")
		if want != got {
			t.Errorf("header.Get(\"X-Custom-Header\") = %q, want %q", got, want)
		}

		assert.Empty(t, resp.Errors)
	})

	t.Run("returns errors for validation failures", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthMethodPath(t, r, "POST", "/stacks/stack-123/notifications")

			w.WriteHeader(http.StatusOK)
			assert.NoError(t, json.NewEncoder(w).Encode(CreateStackNotificationsResponse{
				Errors: []StackNotificationError{
					{
						Error:   "detail is required",
						Indexes: []int{1},
					},
					{
						Error:   "detail exceeds its length limit of 256",
						Indexes: []int{3},
					},
				},
			}))
		})
		t.Cleanup(func() { server.Close() })

		req := CreateStackNotificationsRequest{
			StackKey: "stack-123",
			Notifications: []StackNotification{
				{JobUUID: "456", Detail: "Valid"},
				{JobUUID: "789", Detail: ""},
				{JobUUID: "abc", Detail: "Another valid"},
				{JobUUID: "def", Detail: string(make([]byte, 257))},
			},
		}

		resp, _, err := client.CreateStackNotifications(t.Context(), req)
		assert.NoError(t, err)
		wantErrors := []StackNotificationError{
			{
				Error:   "detail is required",
				Indexes: []int{1},
			},
			{
				Error:   "detail exceeds its length limit of 256",
				Indexes: []int{3},
			},
		}
		if diff := cmp.Diff(resp.Errors, wantErrors); diff != "" {
			t.Errorf("resp.Errors diff (-got +want):\n%s", diff)
		}
	})
}
