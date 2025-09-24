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

	t.Run("respects the Retry-After header", func(t *testing.T) {
		t.Parallel()

		server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "8")
			respondWithError(w, http.StatusTooManyRequests, "Rate limit exceeded")
		})
		t.Cleanup(server.Close)

		sleepDurations := []time.Duration{}
		client.retrierOpts = []roko.RetrierOpt{
			roko.WithMaxAttempts(5),
			roko.WithStrategy(roko.Constant(5 * time.Minute)),
			roko.WithSleepFunc(func(d time.Duration) { sleepDurations = append(sleepDurations, d) }),
		}

		req, err := client.newRequest(t.Context(), "POST", "test", nil)
		if err != nil {
			t.Fatalf("client.newRequest returned error: %v, wanted nil", err)
		}

		_, _, err = do[struct{}](t.Context(), client, req)
		if err == nil {
			t.Fatal("client.do returned nil error, wanted non-nil")
		}

		if len(sleepDurations) != 4 { // 4 retries after the initial request
			t.Fatalf("expected 4 retries, got %d", len(sleepDurations))
		}

		for i, d := range sleepDurations {
			if d != 8*time.Second {
				t.Errorf("sleepDurations[%d] = %v, want 8s", i, d)
			}
		}
	})

	t.Run("retries on transient errors", func(t *testing.T) {
		cases := []struct {
			name          string
			responseCode  int
			callCount     int
			shouldSucceed bool
		}{
			{
				name:          "500 Internal Server Error",
				responseCode:  http.StatusInternalServerError,
				callCount:     3, // Two failures then success
				shouldSucceed: true,
			},
			{
				name:          "429 Too Many Requests",
				responseCode:  http.StatusTooManyRequests,
				callCount:     3, // Two failures then success
				shouldSucceed: true,
			},
			{
				name:          "400 Bad Request",
				responseCode:  http.StatusBadRequest,
				callCount:     1, // One failure, no retries
				shouldSucceed: false,
			},
			{
				name:          "404 Not Found",
				responseCode:  http.StatusNotFound,
				callCount:     1, // One failure, no retries
				shouldSucceed: false,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				callCount := 0
				server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					callCount++
					if callCount < 3 {
						// First two attempts fail with the given status code
						respondWithError(w, tc.responseCode, "Something went wrong")
					} else {
						// Third attempt succeeds
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
					}
				})
				t.Cleanup(func() { server.Close() })

				ctx := context.Background()
				req, err := client.newRequest(ctx, "POST", "test", map[string]string{"key": "value"})
				if err != nil {
					t.Fatalf("failed to create request: %v", err)
				}

				_, _, err = do[map[string]string](ctx, client, req)
				if tc.shouldSucceed {
					if err != nil {
						t.Fatalf("expected success after retries, got error: %v", err)
					}
				} else {
					if err == nil {
						t.Fatal("expected error, got nil")
					}
				}

				if callCount != tc.callCount {
					t.Errorf("callCount = %d, expected %d", callCount, tc.callCount)
				}
			})
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
		t.Cleanup(func() { server.Close() })

		ctx := context.Background()
		req, err := client.newRequest(ctx, "POST", "test", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, _, err = do[struct{}](ctx, client, req)
		if err == nil {
			t.Fatal("expected error after exhausting retries, got nil")
		}

		expectedCalls := 5 // Default max attempts
		if callCount != expectedCalls {
			t.Errorf("callCount = %d, expected %d (exhausted all retries)", callCount, expectedCalls)
		}

		if !strings.Contains(err.Error(), "500") {
			t.Errorf("err.Error() = %v, expected to contain '500'", err)
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
		t.Cleanup(func() { server.Close() })

		// Custom retrier with only 2 attempts
		customRetrier := roko.NewRetrier(
			roko.WithMaxAttempts(2),
			roko.WithStrategy(roko.Constant(1*time.Millisecond)),
			roko.WithSleepFunc(func(time.Duration) {}), // No sleep for fast test
		)

		ctx := context.Background()
		req, err := client.newRequest(ctx, "POST", "test", map[string]string{"key": "value"},
			WithRetrier(customRetrier),
		)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		_, _, err = do[struct{}](ctx, client, req)
		if err == nil {
			t.Fatal("expected error after exhausting retries, got nil")
		}

		if callCount != 2 {
			t.Errorf("callCount = %d, expected 2 (custom max attempts)", callCount)
		}
	})
}
