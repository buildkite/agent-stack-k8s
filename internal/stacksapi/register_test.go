package stacksapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestRegisterStack(t *testing.T) {
	t.Parallel()
	t.Run("successful registration", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "POST", "/stacks/register")

			// Verify request body
			var params RegisterStackParams
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				t.Fatalf("failed to decode request body: %v", err)
			}

			expectedParams := RegisterStackParams{
				Key:      "test-stack",
				Type:     StackTypeCustom,
				QueueKey: "test-queue",
				Metadata: map[string]string{"env": "test"},
			}

			if diff := cmp.Diff(expectedParams, params); diff != "" {
				t.Errorf("request params mismatch (-want +got):\n%s", diff)
			}

			// Return mock response
			response := Stack{
				ID:              "stack-123",
				OrganizationID:  "org-456",
				ClusterQueueKey: "test-queue",
				Key:             "test-stack",
				Type:            StackTypeCustom,
				Metadata:        map[string]string{"env": "test"},
				LastConnectedOn: nil,
				State:           StackStateConnected,
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		})
		defer server.Close()

		ctx := context.Background()
		params := RegisterStackParams{
			Key:      "test-stack",
			Type:     StackTypeCustom,
			QueueKey: "test-queue",
			Metadata: map[string]string{"env": "test"},
		}

		stack, err := client.RegisterStack(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := &Stack{
			ID:              "stack-123",
			OrganizationID:  "org-456",
			ClusterQueueKey: "test-queue",
			Key:             "test-stack",
			Type:            StackTypeCustom,
			Metadata:        map[string]string{"env": "test"},
			LastConnectedOn: nil,
			State:           StackStateConnected,
			apiClient:       client,
		}

		if diff := cmp.Diff(expected, stack, cmpopts.IgnoreFields(Stack{}, "apiClient")); diff != "" {
			t.Errorf("stack mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusInternalServerError, "Internal server error")
		})
		defer server.Close()

		ctx := context.Background()
		params := RegisterStackParams{
			Key:      "test-stack",
			Type:     StackTypeCustom,
			QueueKey: "test-queue",
			Metadata: map[string]string{},
		}

		stack, err := client.RegisterStack(ctx, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if stack != nil {
			t.Error("expected nil stack on error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected error to contain status code, got: %v", err)
		}
	})
}

func TestDeregister(t *testing.T) {
	t.Parallel()
	t.Run("successful deregistration", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			verifyAuthAndMethod(t, r, "POST", "/stacks/test-stack/deregister")
			w.WriteHeader(http.StatusNoContent)
		})
		defer server.Close()

		stack := &Stack{Key: "test-stack", apiClient: client}

		ctx := context.Background()
		err := stack.Deregister(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		t.Parallel()
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, http.StatusNotFound, "Stack not found")
		})
		defer server.Close()

		stack := &Stack{Key: "nonexistent-stack", apiClient: client}

		ctx := context.Background()
		err := stack.Deregister(ctx)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("expected error to contain status code, got: %v", err)
		}
	})
}
