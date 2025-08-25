package stacksapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/buildkite/roko"
)

func TestNewClient(t *testing.T) {
	t.Parallel()
	t.Run("creates client with defaults", func(t *testing.T) {
		t.Parallel()
		client, err := NewClient("test-token")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if client.baseURL.String() != DefaultBaseURL {
			t.Errorf("expected baseURL %s, got %s", DefaultBaseURL, client.baseURL)
		}

		expectedUA := "agent-stack-k8s/internal/stacksapi/" + version.Version()
		if client.userAgent != expectedUA {
			t.Errorf("expected userAgent %s, got %s", expectedUA, client.userAgent)
		}

		expectedAuth := "Token test-token"
		if client.authHeader != expectedAuth {
			t.Errorf("expected authHeader %s, got %s", expectedAuth, client.authHeader)
		}
	})

	t.Run("applies client options", func(t *testing.T) {
		t.Parallel()
		customURL := urlMustParse("https://api.example.com/")
		customClient := &http.Client{Timeout: 10 * time.Second}
		logger := slog.New(slog.NewTextHandler(nil, nil))

		client, err := NewClient("test-token",
			WithBaseURL(customURL),
			WithHTTPClient(customClient),
			WithLogger(logger),
			PrependToUserAgent("custom-agent"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if client.baseURL.String() != customURL.String() {
			t.Errorf("expected baseURL %s, got %s", customURL, client.baseURL)
		}

		if client.httpClient != customClient {
			t.Error("expected custom HTTP client to be set")
		}

		expectedUA := "custom-agent agent-stack-k8s/internal/stacksapi/" + version.Version()
		if client.userAgent != expectedUA {
			t.Errorf("expected userAgent %s, got %s", expectedUA, client.userAgent)
		}
	})
}

func TestNewRequest(t *testing.T) {
	t.Parallel()
	client, err := NewClient("test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("creates request with proper headers", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		req, err := client.NewRequest(ctx, "GET", "test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Method != "GET" {
			t.Errorf("expected method GET, got %s", req.Method)
		}

		expectedURL := "https://agent.buildkite.com/v3/test"
		if req.URL.String() != expectedURL {
			t.Errorf("expected URL %s, got %s", expectedURL, req.URL.String())
		}

		if req.Header.Get("Authorization") != "Token test-token" {
			t.Error("Authorization header not set correctly")
		}

		if req.Header.Get("Content-Type") != "application/json" {
			t.Error("Content-Type header not set correctly")
		}

		expectedUA := "agent-stack-k8s/internal/stacksapi/" + version.Version()
		if req.Header.Get("User-Agent") != expectedUA {
			t.Errorf("User-Agent header not set correctly, got %s", req.Header.Get("User-Agent"))
		}
	})

	t.Run("encodes JSON body", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		body := map[string]string{"key": "value"}

		req, err := client.NewRequest(ctx, "POST", "test", body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Body == nil {
			t.Error("expected request body to be set")
		}

		jsonBytes, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		expectedJSON := `{"key":"value"}`
		if string(jsonBytes) != expectedJSON {
			t.Errorf("expected JSON body %q, got %q", expectedJSON, string(jsonBytes))
		}
	})

	t.Run("applies request options", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		customRetrier := roko.NewRetrier(roko.WithMaxAttempts(1))

		req, err := client.NewRequest(ctx, "GET", "test", nil, WithRetrier(customRetrier))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.retrier != customRetrier {
			t.Error("expected custom retrier to be set")
		}
	})
}

func TestErrorResponse(t *testing.T) {
	t.Parallel()
	t.Run("formats error message correctly", func(t *testing.T) {
		t.Parallel()
		resp := &http.Response{
			StatusCode: 404,
			Request: &http.Request{
				Method: "GET",
				URL:    urlMustParse("https://api.example.com/test"),
			},
		}

		errResp := &ErrorResponse{
			Response: resp,
			Message:  "Not found",
		}

		expected := "GET https://api.example.com/test: 404 Not found"
		if errResp.Error() != expected {
			t.Errorf("expected error message %q, got %q", expected, errResp.Error())
		}
	})

	t.Run("identifies retryable status codes", func(t *testing.T) {
		t.Parallel()
		testCases := []struct {
			statusCode int
			retryable  bool
		}{
			{200, false},
			{400, false},
			{404, false},
			{429, true},
			{500, true},
			{502, true},
			{503, true},
		}

		for _, tc := range testCases {
			errResp := &ErrorResponse{
				Response: &http.Response{StatusCode: tc.statusCode},
			}

			if errResp.IsRetryableStatus() != tc.retryable {
				t.Errorf("status code %d: expected retryable=%v, got %v",
					tc.statusCode, tc.retryable, errResp.IsRetryableStatus())
			}
		}
	})
}
