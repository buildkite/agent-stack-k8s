package stacksapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/buildkite/roko"
)

func setupTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	server := httptest.NewServer(handler)
	serverURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parsing test server url: %s", err)
	}

	retrierOpts := DefaultRetrierOptions
	retrierOpts = append(retrierOpts, roko.WithSleepFunc(func(time.Duration) {}))
	client, err := NewClient("test-token",
		WithBaseURL(serverURL),
		WithRetrierOptions(retrierOpts...),
	)
	if err != nil {
		t.Fatalf("creating test client: %s", err)
	}

	return server, client
}

func verifyAuthAndMethod(t *testing.T, r *http.Request, expectedMethod, expectedPath string) {
	if r.Method != expectedMethod {
		t.Errorf("expected %s, got %s", expectedMethod, r.Method)
	}
	if r.URL.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, r.URL.Path)
	}
	if auth := r.Header.Get("Authorization"); auth != "Token test-token" {
		t.Errorf("expected Authorization header 'Token test-token', got %s", auth)
	}
}

func respondWithError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
