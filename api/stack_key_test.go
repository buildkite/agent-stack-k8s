package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/buildkite/stacksapi"
)

// Registration keys are organization-wide. This server deliberately models the
// API's upsert behavior: registering the same key replaces its queue association.
func TestControllersSharingIDCanAcquireTokensFromTheirOwnQueues(t *testing.T) {
	var mu sync.Mutex
	stacks := make(map[string]string)
	clusters := map[string]string{
		"Token cluster-1-token":         "cluster-1",
		"Token cluster-1-token-rotated": "cluster-1",
		"Token cluster-2-token":         "cluster-2",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /stacks/register", func(w http.ResponseWriter, r *http.Request) {
		var req stacksapi.RegisterStackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// The token determines the Buildkite cluster; queue keys can repeat across clusters.
		scope := clusters[r.Header.Get("Authorization")] + "/" + req.QueueKey
		mu.Lock()
		stacks[req.Key] = scope
		mu.Unlock()
		writeJSONResponse(w, http.StatusCreated, stacksapi.RegisterStackResponse{Key: req.Key, ClusterQueueKey: req.QueueKey})
	})
	mux.HandleFunc("POST /stacks/{key}/job-acquisition-tokens", func(w http.ResponseWriter, r *http.Request) {
		var req IssueJobAcquisitionTokensRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		scope := stacks[r.PathValue("key")]
		mu.Unlock()
		if len(req.JobUUIDs) != 1 || req.JobUUIDs[0] != scope {
			writeJSONResponse(w, http.StatusForbidden, map[string]string{"message": "job does not belong to the registered stack queue"})
			return
		}
		writeJSONResponse(w, http.StatusCreated, IssueJobAcquisitionTokensResponse{JobAcquisitionTokens: []IssuedJobAcquisitionToken{{JobUUID: req.JobUUIDs[0], JobAcquisitionToken: "issued-token"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	var clients []*AgentClient
	for _, opts := range []AgentClientOpts{
		{ClusterID: "cluster-1", Queue: "production", Token: "cluster-1-token"},
		{ClusterID: "cluster-1", Queue: "staging", Token: "cluster-1-token"},
		{ClusterID: "cluster-2", Queue: "production", Token: "cluster-2-token"},
		{ClusterID: "cluster-1", Queue: "production", Token: "cluster-1-token-rotated"},
	} {
		opts.StackID = "identical-helm-release-name"
		opts.Endpoint = server.URL
		opts.Logger = slog.Default()
		client, err := NewAgentClient(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
	}
	if clients[0].stack.Key != clients[3].stack.Key {
		t.Error("restarting with a rotated token changed the stack key")
	}
	// All registrations must finish before issuing tokens, so the second/third
	// controller cannot silently steal the first controller's stack registration.
	for _, client := range clients {
		jobID := client.clusterID + "/" + client.queue
		response, _, err := client.IssueJobAcquisitionTokens(t.Context(), []string{jobID}, 0)
		if err != nil {
			t.Errorf("cluster %s queue %s: %v", client.clusterID, client.queue, err)
			continue
		}
		if len(response.JobAcquisitionTokens) != 1 {
			t.Errorf("cluster %s queue %s: token not issued", client.clusterID, client.queue)
		}
	}
}

func TestStackKeyScopeIsStableAndUnambiguous(t *testing.T) {
	base := scopedStackKey("cluster", "queue", "controller")
	tests := []struct {
		name, cluster, queue, id string
		same                     bool
	}{
		{"restart", "cluster", "queue", "controller", true},
		{"different cluster", "other", "queue", "controller", false},
		{"different queue", "cluster", "other", "controller", false},
		{"different controller", "cluster", "queue", "other", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := scopedStackKey(test.cluster, test.queue, test.id)
			if (got == base) != test.same {
				t.Errorf("key equality = %t, want %t", got == base, test.same)
			}
		})
	}
	for _, queue := range []string{"", "~", "-", stacksapi.DefaultQueue} {
		if scopedStackKey("cluster", queue, "controller") != scopedStackKey("cluster", stacksapi.DefaultQueue, "controller") {
			t.Errorf("default queue alias %q changes identity", queue)
		}
	}
	if scopedStackKey("a/b", "c", "d") == scopedStackKey("a", "b/c", "d") {
		t.Error("scope components are ambiguous")
	}
	prefix := strings.Repeat("a", 63)
	a, b := scopedStackKey("cluster", "queue", prefix+"a"), scopedStackKey("cluster", "queue", prefix+"b")
	if a == b {
		t.Error("IDs differing beyond the display prefix collide")
	}
	for _, key := range []string{base, a, b} {
		if len(key) > 63 {
			t.Errorf("key has %d characters, want at most 63", len(key))
		}
	}
}
