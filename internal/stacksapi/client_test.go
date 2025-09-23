package stacksapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/buildkite/roko"
	"github.com/google/go-cmp/cmp"
)

func TestNewClient(t *testing.T) {
	t.Parallel()
	t.Run("creates client with defaults", func(t *testing.T) {
		t.Parallel()
		client, err := NewClient("test-token")
		if err != nil {
			t.Fatalf(`NewClient("test-token") error = %v, expected nil`, err)
		}

		if client.baseURL.String() != DefaultBaseURL {
			t.Errorf("client.BaseURL.String() = %q, expected %q", client.baseURL.String(), DefaultBaseURL)
		}

		expectedUA := "agent-stack-k8s/" + version.Version()
		if client.userAgent != expectedUA {
			t.Errorf("client.userAgent = %s, expected %s", client.userAgent, expectedUA)
		}

		expectedAuth := "Token test-token"
		if client.authHeader != expectedAuth {
			t.Errorf("client.authHeader = %s, expected %s", client.authHeader, expectedAuth)
		}
	})

	t.Run("applies client options", func(t *testing.T) {
		t.Parallel()
		customURL := urlMustParse("https://api.example.com/")
		customClient := &http.Client{Timeout: 10 * time.Second}
		logger := slog.New(slog.NewTextHandler(nil, nil))

		opts := []ClientOpt{
			WithBaseURL(customURL),
			WithHTTPClient(customClient),
			WithLogger(logger),
			PrependToUserAgent("custom-agent"),
		}

		client, err := NewClient("test-token", opts...)
		if err != nil {
			t.Fatalf(`NewClient("test-token", opts...) error = %v, expected nil`, err)
		}

		if client.baseURL.String() != customURL.String() {
			t.Errorf("client.baseURL.String() = %q, expected %q", client.baseURL.String(), customURL.String())
		}

		if diff := cmp.Diff(client.httpClient, customClient); diff != "" {
			t.Error("client.httpClient mismatch (-want +got):\n" + diff)
		}

		expectedUA := "custom-agent agent-stack-k8s/" + version.Version()
		if client.userAgent != expectedUA {
			t.Errorf("client.userAgent = %q, expected %q", client.userAgent, expectedUA)
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
		req, err := client.newRequest(ctx, "GET", "test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.Method != "GET" {
			t.Errorf("req.Method = %s, expected GET", req.Method)
		}

		expectedURL := "https://agent.buildkite.com/v3/test"
		if req.URL.String() != expectedURL {
			t.Errorf("req.URL.String() = %s, expected %s", req.URL.String(), expectedURL)
		}

		if req.Header.Get("Authorization") != "Token test-token" {
			t.Errorf("req.Header.Get(\"Authorization\") = %q, expected %q", req.Header.Get("Authorization"), "Token test-token")
		}

		if req.Header.Get("Content-Type") != "application/json" {
			t.Errorf("req.Header.Get(\"Content-Type\") = %q, expected %q", req.Header.Get("Content-Type"), "application/json")
		}

		expectedUA := "agent-stack-k8s/" + version.Version()
		if req.Header.Get("User-Agent") != expectedUA {
			t.Errorf("req.Header.Get(\"User-Agent\") = %s, expected %s", req.Header.Get("User-Agent"), expectedUA)
		}
	})

	t.Run("encodes JSON body", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		body := map[string]string{"key": "value"}

		req, err := client.newRequest(ctx, "POST", "test", body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		jsonBytes, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		expectedJSON := `{"key":"value"}`
		if string(jsonBytes) != expectedJSON {
			t.Errorf("JSON body = %q, expected %q", string(jsonBytes), expectedJSON)
		}
	})

	t.Run("applies request options", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		customRetrier := roko.NewRetrier(roko.WithMaxAttempts(1))

		req, err := client.newRequest(ctx, "GET", "test", nil, WithRetrier(customRetrier))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if req.retrier != customRetrier {
			t.Errorf("req.retrier = %p, expected %p", req.retrier, customRetrier)
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
			t.Errorf("errResp.Error() = %q, expected %q", errResp.Error(), expected)
		}
	})

	t.Run("parses errors and messages from server responses", func(t *testing.T) {
		t.Parallel()
		testCases := []struct {
			statusCode int
			message    string
		}{
			{404, "There is no stack by that name here"},
			{500, "Something went wrong processing your request"},
			{403, "Eeep! You forgot your token"},
			{429, "Too many requests"},
		}

		for _, tc := range testCases {
			t.Run(http.StatusText(tc.statusCode), func(t *testing.T) {
				server, client := setupTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					respondWithError(w, tc.statusCode, tc.message)
				})
				t.Cleanup(func() { server.Close() })

				req, err := client.newRequest(t.Context(), "GET", "test", nil)
				if err != nil {
					t.Fatalf(`client.NewRequest(t.Context(), "GET", "test", nil): %v, expected nil`, err)
				}

				_, _, err = do[struct{}](t.Context(), client, req)
				if err == nil {
					t.Fatalf("do[struct{}](t.Context(), client, req) error = %v, expected error", err)
				}

				// Check the error response
				var errResp *ErrorResponse
				ok := errors.As(err, &errResp)
				if !ok {
					t.Fatalf(`err from client.do(t.Context(), req, nil) was: %v, expected ErrorResponse`, err)
				}

				if errResp.Message != tc.message {
					t.Errorf("errResp.Message = %q, expected %q", errResp.Message, tc.message)
				}

				if errResp.Response.StatusCode != tc.statusCode {
					t.Errorf("errResp.Response.StatusCode = %d, expected %d", errResp.Response.StatusCode, tc.statusCode)
				}
			})
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
			{500, true},
			{502, true},
			{503, true},
			{429, true},
		}

		for _, tc := range testCases {
			errResp := &ErrorResponse{
				Response: &http.Response{StatusCode: tc.statusCode},
			}

			if errResp.IsRetryableStatus() != tc.retryable {
				t.Errorf("status code %d: IsRetryableStatus() = %v, expected %v",
					tc.statusCode, errResp.IsRetryableStatus(), tc.retryable)
			}
		}
	})
}
