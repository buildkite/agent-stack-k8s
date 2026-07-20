package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
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

	// ReserveExpirySeconds records the reservation_expiry_seconds value from each ReserveJobs call.
	ReserveExpirySeconds []int

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

	// JobStates maps job UUIDs to their state strings for GetJobStates.
	JobStates map[string]string

	// GetJobStateCalls records the job UUIDs from each GetJobStates call.
	GetJobStateCalls [][]string

	// GetJobStatesStatusCode configures the HTTP status code for GetJobStates.
	// Default is 200.
	GetJobStatesStatusCode int

	// GetJobStatesError configures an error message to return for GetJobStates.
	GetJobStatesError string

	// FinishJobCalls records the job UUIDs from each FinishJob call.
	FinishJobCalls []string

	// FinishJobStatusCode configures the HTTP status code for FinishJob.
	// Default is 200.
	FinishJobStatusCode int

	// OnFinishJob is an optional callback invoked after recording a FinishJob
	// call but before sending the response. Useful for simulating state changes
	// that happen between API calls (e.g., an agent acquiring a job).
	OnFinishJob func(jobUUID string)

	// FinishJobError configures an error message to return for FinishJob.
	FinishJobError string
}

// NewFakeAgentServer creates and starts a fake agent API server.
// Use server.URL() to get the endpoint for creating a real AgentClient.
func NewFakeAgentServer() *FakeAgentServer {
	fake := &FakeAgentServer{
		ReserveStatusCode:      http.StatusOK,
		GetJobStatesStatusCode: http.StatusOK,
		FinishJobStatusCode:    http.StatusOK,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stacks/register", fake.handleRegisterStack)
	mux.HandleFunc("/stacks/test-stack/scheduled-jobs/batch-reserve", fake.handleReserveJobs)
	mux.HandleFunc("/stacks/test-stack/notifications", fake.handleNotifications)
	mux.HandleFunc("/stacks/test-stack/jobs/get-states", fake.handleGetJobStates)
	mux.HandleFunc("/stacks/test-stack/jobs/", fake.handleFinishJob)

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
	f.ReserveExpirySeconds = append(f.ReserveExpirySeconds, req.ReservationExpirySeconds)
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

func (f *FakeAgentServer) handleGetJobStates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		StackKey string   `json:"stack_key"`
		JobUUIDs []string `json:"job_uuids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.GetJobStateCalls = append(f.GetJobStateCalls, req.JobUUIDs)

	if f.GetJobStatesError != "" {
		f.mu.Unlock()
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(map[string]string{
			"message": f.GetJobStatesError,
		}); err != nil {
			http.Error(w, "fake: failed to encode error response: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.GetJobStatesStatusCode)
		if _, err := w.Write(buf.Bytes()); err != nil {
			log.Printf("fake: failed to write get job states error response: %v", err)
		}
		return
	}

	states := make(map[string]string)
	for _, id := range req.JobUUIDs {
		if s, ok := f.JobStates[id]; ok {
			states[id] = s
		}
	}
	f.mu.Unlock()

	resp := struct {
		States map[string]string `json:"states"`
	}{States: states}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		http.Error(w, "fake: failed to encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.GetJobStatesStatusCode)
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Printf("fake: failed to write get job states response: %v", err)
	}
}

func (f *FakeAgentServer) handleFinishJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path: /stacks/test-stack/jobs/{uuid}/finish
	path := r.URL.Path
	const prefix = "/stacks/test-stack/jobs/"
	const suffix = "/finish"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	jobUUID := path[len(prefix) : len(path)-len(suffix)]

	f.mu.Lock()
	f.FinishJobCalls = append(f.FinishJobCalls, jobUUID)
	if f.OnFinishJob != nil {
		f.OnFinishJob(jobUUID)
	}
	f.mu.Unlock()

	if f.FinishJobError != "" {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(map[string]string{
			"message": f.FinishJobError,
		}); err != nil {
			http.Error(w, "fake: failed to encode error response: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.FinishJobStatusCode)
		if _, err := w.Write(buf.Bytes()); err != nil {
			log.Printf("fake: failed to write finish job error response: %v", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.FinishJobStatusCode)
	if _, err := w.Write([]byte("{}\n")); err != nil {
		log.Printf("fake: failed to write finish job response: %v", err)
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
