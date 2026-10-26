package reserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/stacksapi"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handlerFunc func(context.Context, *api.AgentScheduledJob) error

func (f handlerFunc) Handle(ctx context.Context, job *api.AgentScheduledJob) error {
	return f(ctx, job)
}

func TestReserver(t *testing.T) {
	schedulerErr := errors.New("scheduling failed")
	for _, tc := range []struct {
		name         string
		response     *stacksapi.BatchReserveJobsResponse
		status       int
		apiError     string
		schedulerErr error
		wantHandled  bool
		wantError    bool
	}{
		{name: "reserved", wantHandled: true},
		{name: "reserved elsewhere", response: &stacksapi.BatchReserveJobsResponse{}, wantError: true},
		{name: "API failure", status: http.StatusServiceUnavailable, apiError: "unavailable", wantError: true},
		{name: "scheduling failure", schedulerErr: schedulerErr, wantHandled: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := api.NewFakeAgentServer()
			defer server.Close()
			server.ReserveResponse = tc.response
			server.ReserveError = tc.apiError
			if tc.status != 0 {
				server.ReserveStatusCode = tc.status
			}
			client, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
				Token: "fake-token", Endpoint: server.URL(), StackID: "test-stack", Logger: slog.Default(),
			})
			if err != nil {
				t.Fatal(err)
			}
			job := &api.AgentScheduledJob{ID: uuid.NewString()}
			var handled *api.AgentScheduledJob
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			r := New(logger, client, handlerFunc(func(_ context.Context, job *api.AgentScheduledJob) error {
				handled = job
				return tc.schedulerErr
			}), 1800)
			err = r.Handle(t.Context(), job)
			if (err != nil) != tc.wantError {
				t.Fatalf("Handle() error = %v, want error %v", err, tc.wantError)
			}
			if tc.schedulerErr != nil && !errors.Is(err, tc.schedulerErr) {
				t.Errorf("Handle() error = %v, want %v", err, tc.schedulerErr)
			}
			if tc.wantHandled && handled != job {
				t.Error("reserved job did not reach the scheduler")
			}
			if !tc.wantHandled && handled != nil {
				t.Error("unreserved job reached the scheduler")
			}
			if len(server.ReserveCalls) != 1 || !slices.Equal(server.ReserveCalls[0], []string{job.ID}) {
				t.Fatalf("reserved jobs = %v, want only %s", server.ReserveCalls, job.ID)
			}
			if got := server.ReserveExpirySeconds[0]; got != 1800 {
				t.Errorf("reservation expiry = %d, want 1800", got)
			}
			if tc.apiError == "" {
				if logs.Len() != 0 {
					t.Errorf("unexpected default-level reservation log: %s", &logs)
				}
				return
			}
			var entry struct {
				Level   string
				JobUUID string `json:"job-uuid"`
				Error   string
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("reservation API failure did not produce a default-level log: %v", err)
			}
			if entry.Level != "WARN" || entry.JobUUID != job.ID || !strings.Contains(entry.Error, tc.apiError) {
				t.Errorf("reservation warning = %+v, want WARN with job %s and API error %q", entry, job.ID, tc.apiError)
			}
		})
	}
}

func TestReservationsRespectMaxInFlight(t *testing.T) {
	for _, limit := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// Model exclusive reservations shared by two independent stacks.
			var mu sync.Mutex
			owners := make(map[string]string)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /stacks/register", func(w http.ResponseWriter, req *http.Request) {
				var registration stacksapi.RegisterStackRequest
				if err := json.NewDecoder(req.Body).Decode(&registration); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(stacksapi.RegisterStackResponse{Key: registration.Key})
			})
			mux.HandleFunc("PUT /stacks/{stack}/scheduled-jobs/batch-reserve", func(w http.ResponseWriter, req *http.Request) {
				var reservation stacksapi.BatchReserveJobsRequest
				if err := json.NewDecoder(req.Body).Decode(&reservation); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				var response stacksapi.BatchReserveJobsResponse
				for _, id := range reservation.JobUUIDs {
					if _, reserved := owners[id]; reserved {
						response.NotReserved = append(response.NotReserved, id)
					} else {
						owners[id] = req.PathValue("stack")
						response.Reserved = append(response.Reserved, id)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			client, err := api.NewAgentClient(ctx, api.AgentClientOpts{
				Token: "fake-token", Endpoint: server.URL, StackID: "capped", Logger: slog.Default(),
			})
			if err != nil {
				t.Fatal(err)
			}
			jobs := []*api.AgentScheduledJob{
				{ID: uuid.NewString()}, {ID: uuid.NewString()}, {ID: uuid.NewString()},
				{ID: uuid.NewString()}, {ID: uuid.NewString()},
			}
			scheduled := make(chan *api.AgentScheduledJob, len(jobs))
			r := New(slog.Default(), client, handlerFunc(func(_ context.Context, job *api.AgentScheduledJob) error {
				scheduled <- job
				return nil
			}), 0)
			l := limiter.New(ctx, slog.Default(), r, limit, 3, -1)
			defer func() { cancel(); l.Wait() }()
			if err := l.HandleMany(ctx, jobs); err != nil {
				t.Fatal(err)
			}
			want := limit
			if want == 0 {
				want = len(jobs)
			}
			var first *api.AgentScheduledJob
			for range want {
				select {
				case first = <-scheduled:
				case <-time.After(5 * time.Second):
					t.Fatal("available capacity was not used")
				}
			}
			if limit > 0 {
				peerClient, err := api.NewAgentClient(ctx, api.AgentClientOpts{
					Token: "fake-token", Endpoint: server.URL, StackID: "peer", Logger: slog.Default(),
				})
				if err != nil {
					t.Fatal(err)
				}
				peerScheduled := make(chan string, len(jobs))
				peerReserver := New(slog.Default(), peerClient, handlerFunc(func(_ context.Context, job *api.AgentScheduledJob) error {
					peerScheduled <- job.ID
					return nil
				}), 0)
				peer := limiter.New(ctx, slog.Default(), peerReserver, 0, 3, -1)
				defer func() { cancel(); peer.Wait() }()
				if err := peer.HandleMany(ctx, jobs); err != nil {
					t.Fatal(err)
				}
				for range len(jobs) - limit {
					select {
					case <-peerScheduled:
					case <-time.After(5 * time.Second):
						t.Fatal("peer could not reserve excess jobs while capped stack was full")
					}
				}
				// Add fresh work: the peer now holds the original excess jobs.
				fresh := &api.AgentScheduledJob{ID: uuid.NewString()}
				if err := l.HandleMany(ctx, []*api.AgentScheduledJob{fresh}); err != nil {
					t.Fatal(err)
				}
				// Completion should free one slot and allow one more reservation.
				l.OnUpdate(&batchv1.Job{}, &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{config.UUIDLabel: first.ID}},
					Status: batchv1.JobStatus{
						Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete}},
					},
				})
				select {
				case next := <-scheduled:
					if next.ID != fresh.ID {
						t.Errorf("scheduled %s after completing %s, want fresh job %s", next.ID, first.ID, fresh.ID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("completion did not free capacity")
				}
				want++
			}
			// Stop the capped stack before inspecting its reservations.
			cancel()
			l.Wait()
			mu.Lock()
			defer mu.Unlock()
			reserved := 0
			for _, owner := range owners {
				if owner == "capped" {
					reserved++
				}
			}
			if reserved != want {
				t.Errorf("reserved %d jobs, want %d; excess jobs must remain available to other stacks", reserved, want)
			}
		})
	}
}
