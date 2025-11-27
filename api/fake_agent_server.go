package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/buildkite/stacksapi"
)

// FakeAgentServer is an httptest.Server that implements the Buildkite Agent API.
// It records all requests for verification in tests.
// Use with a real AgentClient for realistic integration testing.
type FakeAgentServer struct {
	server *httptest.Server
	mu     sync.Mutex

	// ReserveCalls records the job IDs passed to each ReserveJobs call.
	ReserveCalls [][]string

	// ReserveResponse configures the response for ReserveJobs.
	// If nil, returns all job IDs as reserved.
	ReserveResponse *stacksapi.BatchReserveJobsResponse

	// ReserveStatusCode configures the HTTP status code for ReserveJobs.
	// Default is 200.
	ReserveStatusCode int

	// ReserveError configures an error message to return.
	ReserveError string

	// NotificationCalls records all notification batches sent to the server.
	NotificationCalls [][]stacksapi.StackNotification
}

// NewFakeAgentServer creates and starts a fake agent API server.
// Use server.URL() to get the endpoint for creating a real AgentClient.
func NewFakeAgentServer() *FakeAgentServer {
	fake := &FakeAgentServer{
		ReserveStatusCode: http.StatusOK,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stacks/register", fake.handleRegisterStack)
	mux.HandleFunc("/stacks/test-stack/scheduled-jobs/batch-reserve", fake.handleReserveJobs)
	mux.HandleFunc("/stacks/test-stack/notifications", fake.handleNotifications)

	fake.server = httptest.NewServer(mux)
	return fake
}

// URL returns the base URL of the fake server.
// Use this as the Endpoint when creating a real AgentClient.
func (f *FakeAgentServer) URL() string {
	return f.server.URL
}

// Close shuts down the fake server.
func (f *FakeAgentServer) Close() {
	f.server.Close()
}

func (f *FakeAgentServer) handleRegisterStack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stacksapi.RegisterStackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := stacksapi.RegisterStackResponse{
		Key:             req.Key,
		ClusterQueueKey: req.QueueKey,
		Metadata:        req.Metadata,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		http.Error(w, "fake: failed to encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Printf("fake: failed to write register stack response: %v", err)
	}
}

func (f *FakeAgentServer) handleReserveJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stacksapi.BatchReserveJobsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Record the call (mutex protects concurrent access from HTTP handler)
	f.mu.Lock()
	f.ReserveCalls = append(f.ReserveCalls, req.JobUUIDs)
	f.mu.Unlock()

	// Return configured error if set
	if f.ReserveError != "" {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(map[string]string{
			"message": f.ReserveError,
		}); err != nil {
			http.Error(w, "fake: failed to encode error response: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.ReserveStatusCode)
		if _, err := w.Write(buf.Bytes()); err != nil {
			log.Printf("fake: failed to write error response: %v", err)
		}
		return
	}

	// Return configured response
	resp := f.ReserveResponse
	if resp == nil {
		// Default: all jobs reserved successfully
		resp = &stacksapi.BatchReserveJobsResponse{
			Reserved:    req.JobUUIDs,
			NotReserved: []string{},
		}
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		http.Error(w, "fake: failed to encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.ReserveStatusCode)
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Printf("fake: failed to write reserve jobs response: %v", err)
	}
}

func (f *FakeAgentServer) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req stacksapi.CreateStackNotificationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.NotificationCalls = append(f.NotificationCalls, req.Notifications)
	f.mu.Unlock()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(stacksapi.CreateStackNotificationsResponse{}); err != nil {
		http.Error(w, "fake: failed to encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Printf("fake: failed to write notifications response: %v", err)
	}
}
