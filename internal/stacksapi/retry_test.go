package stacksapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/roko"
)

func TestRetryLogic(t *testing.T) {
	t.Parallel()

	t.Run("retries on 500 errors", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount < 3 {
				// First two attempts fail with 500
				respondWithError(w, http.StatusInternalServerError, "Server error")
			} else {
				// Third attempt succeeds
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
			}
		})
		defer server.Close()

		ctx := context.Background()
		req, err := client.NewRequest(ctx, "POST", "test", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		var response map[string]string
		_, err = client.Do(ctx, req, &response)
		if err != nil {
			t.Fatalf("expected success after retries, got error: %v", err)
		}

		if callCount != 3 {
			t.Errorf("expected 3 calls (2 failures + 1 success), got %d", callCount)
		}

		if response["status"] != "success" {
			t.Errorf("expected response status 'success', got %v", response)
		}
	})

	t.Run("retries on 429 Too Many Requests", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount < 2 {
				// First attempt fails with 429
				respondWithError(w, http.StatusTooManyRequests, "Rate limited")
			} else {
				// Second attempt succeeds
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
			}
		})
		defer server.Close()

		ctx := context.Background()
		req, err := client.NewRequest(ctx, "POST", "test", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		var response map[string]string
		_, err = client.Do(ctx, req, &response)
		if err != nil {
			t.Fatalf("expected success after retry, got error: %v", err)
		}

		if callCount != 2 {
			t.Errorf("expected 2 calls (1 failure + 1 success), got %d", callCount)
		}
	})

	t.Run("does not retry on 400 Bad Request", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			respondWithError(w, http.StatusBadRequest, "Bad request")
		})
		defer server.Close()

		ctx := context.Background()
		req, err := client.NewRequest(ctx, "POST", "test", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, err = client.Do(ctx, req, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if callCount != 1 {
			t.Errorf("expected 1 call (no retries on 400), got %d", callCount)
		}

		if !strings.Contains(err.Error(), "400") {
			t.Errorf("expected error to contain '400', got: %v", err)
		}
	})

	t.Run("does not retry on 404 Not Found", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			respondWithError(w, http.StatusNotFound, "Not found")
		})
		defer server.Close()

		ctx := context.Background()
		req, err := client.NewRequest(ctx, "GET", "nonexistent", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, err = client.Do(ctx, req, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if callCount != 1 {
			t.Errorf("expected 1 call (no retries on 404), got %d", callCount)
		}
	})

	t.Run("eventually gives up after max retries", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			// Always return 500 to exhaust retries
			respondWithError(w, http.StatusInternalServerError, "Persistent error")
		})
		defer server.Close()

		ctx := context.Background()
		req, err := client.NewRequest(ctx, "POST", "test", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, err = client.Do(ctx, req, nil)
		if err == nil {
			t.Fatal("expected error after exhausting retries, got nil")
		}

		expectedCalls := 5 // Default max attempts
		if callCount != expectedCalls {
			t.Errorf("expected %d calls (exhausted all retries), got %d", expectedCalls, callCount)
		}

		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected error to contain '500', got: %v", err)
		}
	})

	t.Run("preserves request body across retries", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		var receivedBodies []map[string]string

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++

			// Read and store the request body
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				receivedBodies = append(receivedBodies, body)
			}

			if callCount < 3 {
				// First two attempts fail
				respondWithError(w, http.StatusInternalServerError, "Server error")
			} else {
				// Third attempt succeeds
				w.WriteHeader(http.StatusOK)
			}
		})
		defer server.Close()

		ctx := context.Background()
		expectedBody := map[string]string{"test": "data", "retry": "test"}
		req, err := client.NewRequest(ctx, "POST", "test", expectedBody)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, err = client.Do(ctx, req, nil)
		if err != nil {
			t.Fatalf("expected success after retries, got error: %v", err)
		}

		if callCount != 3 {
			t.Errorf("expected 3 calls, got %d", callCount)
		}

		if len(receivedBodies) != 3 {
			t.Fatalf("expected 3 received bodies, got %d", len(receivedBodies))
		}

		// Verify all requests had the same body
		for i, body := range receivedBodies {
			if body["test"] != expectedBody["test"] || body["retry"] != expectedBody["retry"] {
				t.Errorf("request %d body mismatch: expected %v, got %v", i+1, expectedBody, body)
			}
		}
	})

	t.Run("custom retrier configuration", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			callCount++
			// Always fail to test max attempts
			respondWithError(w, http.StatusInternalServerError, "Server error")
		})
		defer server.Close()

		// Custom retrier with only 2 attempts
		customRetrier := roko.NewRetrier(
			roko.WithMaxAttempts(2),
			roko.WithStrategy(roko.Constant(1*time.Millisecond)),
			roko.WithSleepFunc(func(time.Duration) {}), // No sleep for fast test
		)

		ctx := context.Background()
		req, err := client.NewRequest(ctx, "POST", "test",
			map[string]string{"key": "value"},
			WithRetrier(customRetrier),
		)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, err = client.Do(ctx, req, nil)
		if err == nil {
			t.Fatal("expected error after exhausting retries, got nil")
		}

		if callCount != 2 {
			t.Errorf("expected 2 calls (custom max attempts), got %d", callCount)
		}
	})
}
