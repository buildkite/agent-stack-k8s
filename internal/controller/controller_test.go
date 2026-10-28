package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/stacksapi"
	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestRunDrainsAcceptedDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		block  string
		tokens bool
	}{
		{name: "job information", block: "info", tokens: true},
		{name: "Kubernetes Job", block: "jobs", tokens: true},
		{name: "token Secret", block: "secrets", tokens: true},
		{name: "shared token", block: "info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted1, accepted2, queued := uuid.NewString(), uuid.NewString(), uuid.NewString()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			blocked := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			block := func() { blocked <- struct{}{}; <-release }
			reserved := make(chan struct{}, 2)
			var polls, reservations, infos, deregistrations atomic.Int32
			mux := http.NewServeMux()
			respond := func(w http.ResponseWriter, value any) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}
			mux.HandleFunc("GET /token", func(w http.ResponseWriter, _ *http.Request) {
				respond(w, api.AgentTokenIdentity{ClusterUUID: "cluster"})
			})
			mux.HandleFunc("POST /stacks/register", func(w http.ResponseWriter, _ *http.Request) {
				respond(w, stacksapi.RegisterStackResponse{Key: "test-stack", ClusterQueueKey: "test"})
			})
			mux.HandleFunc("GET /stacks/test-stack/scheduled-jobs", func(w http.ResponseWriter, _ *http.Request) {
				polls.Add(1)
				respond(w, stacksapi.ListScheduledJobsResponse{Jobs: []stacksapi.ScheduledJob{
					{ID: accepted1}, {ID: accepted2}, {ID: accepted1}, {ID: queued},
				}})
			})
			mux.HandleFunc("PUT /stacks/test-stack/scheduled-jobs/batch-reserve", func(w http.ResponseWriter, r *http.Request) {
				var req stacksapi.BatchReserveJobsRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				reservations.Add(int32(len(req.JobUUIDs)))
				reserved <- struct{}{}
				respond(w, stacksapi.BatchReserveJobsResponse{Reserved: req.JobUUIDs})
			})
			mux.HandleFunc("POST /stacks/test-stack/job-acquisition-tokens", func(w http.ResponseWriter, r *http.Request) {
				var req api.IssueJobAcquisitionTokensRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				var resp api.IssueJobAcquisitionTokensResponse
				for _, id := range req.JobUUIDs {
					resp.JobAcquisitionTokens = append(resp.JobAcquisitionTokens, api.IssuedJobAcquisitionToken{JobUUID: id, JobAcquisitionToken: "test-jat"})
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				respond(w, resp)
			})
			mux.HandleFunc("GET /stacks/test-stack/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
				infos.Add(1)
				if tc.block == "info" {
					blocked <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				respond(w, api.AgentJob{ID: r.PathValue("id"), Command: "true"})
			})
			mux.HandleFunc("POST /stacks/test-stack/notifications", func(w http.ResponseWriter, _ *http.Request) {
				respond(w, stacksapi.CreateStackNotificationsResponse{})
			})
			mux.HandleFunc("POST /stacks/test-stack/deregister", func(w http.ResponseWriter, _ *http.Request) {
				deregistrations.Add(1)
				respond(w, struct{}{})
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			endpoint := server.URL
			k8s := fake.NewClientset(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-token", Namespace: "test"},
				Data:       map[string][]byte{agentTokenKey: []byte("fake-token")},
			})
			k8s.PrependReactor("create", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if action.GetResource().Resource == tc.block {
					block()
				}
				if job, ok := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job); ok {
					job.UID = types.UID(job.Name)
				}
				return false, nil, nil
			})
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, slog.Default(), k8s, &config.Config{
					Namespace: "test", AgentTokenSecret: "agent-token", ID: "test-stack",
					Queue: "test", Image: "buildkite/agent:latest", JobPrefix: "buildkite-",
					AgentConfig: &config.AgentConfig{Endpoint: &endpoint},
					MaxInFlight: 3, JobCreationConcurrency: 2, WorkQueueLimit: 10,
					PollInterval: time.Second, PaginationPageSize: 100, PaginationDepthLimit: 1,
					EnableJobAcquisitionTokens: tc.tokens,
				})
			}()
			defer func() {
				cancel()
				unblock()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("controller failed to stop during cleanup")
				}
			}()
			for range 2 {
				select {
				case <-reserved:
				case <-time.After(5 * time.Second):
					t.Fatal("controller did not reserve accepted jobs")
				}
			}
			wantBlocked := 1 // The fake Kubernetes client serializes reactors.
			if tc.block == "info" {
				wantBlocked = 2
			}
			for range wantBlocked {
				select {
				case <-blocked:
				case <-time.After(5 * time.Second):
					t.Fatal("dispatch did not reach the blocked call")
				}
			}
			cancel() // The same context cancellation used by the SIGTERM handler.
			select {
			case <-done:
				t.Error("controller exited before accepted dispatch completed")
			case <-time.After(100 * time.Millisecond):
			}
			unblock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("controller waited beyond dispatch (workloads never complete in this test)")
			}
			jobs, err := k8s.BatchV1().Jobs("test").List(t.Context(), metav1.ListOptions{})
			if err != nil || len(jobs.Items) != 2 {
				t.Fatalf("created Jobs = %v, error = %v; want two", jobs, err)
			}
			for _, job := range jobs.Items {
				if id := job.Labels[config.UUIDLabel]; id != accepted1 && id != accepted2 {
					t.Errorf("unexpected dispatched job %q", id)
				}
				if tc.tokens {
					secret, err := k8s.CoreV1().Secrets("test").Get(t.Context(), job.Annotations[config.JobAcquisitionTokenSecretAnnotation], metav1.GetOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if string(secret.Data[agentTokenKey]) != "test-jat" || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != job.UID {
						t.Errorf("token Secret does not belong to accepted Job: %+v", secret)
					}
				}
			}
			creates := map[string]int{}
			for _, action := range k8s.Actions() {
				if action.GetVerb() == "create" {
					creates[action.GetResource().Resource]++
				}
			}
			wantSecrets := 0
			if tc.tokens {
				wantSecrets = 2
			}
			if creates["jobs"] != 2 || creates["secrets"] != wantSecrets {
				t.Errorf("creation attempts = %v; want exactly two Jobs and %d Secrets", creates, wantSecrets)
			}
			if polls.Load() != 1 || reservations.Load() != 2 || infos.Load() != 2 || deregistrations.Load() != 1 {
				t.Errorf("polls/reservations/infos/deregistrations = %d/%d/%d/%d; want 1/2/2/1", polls.Load(), reservations.Load(), infos.Load(), deregistrations.Load())
			}
		})
	}
}

func TestInformerTransformPreservesJobAcquisitionTokenSecretAnnotation(t *testing.T) {
	kjob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		config.JobAcquisitionTokenSecretAnnotation: "job-token",
		"unneeded": "annotation",
	}}}

	transformed, err := informerTransform(kjob)
	if err != nil {
		t.Fatalf("informerTransform() error = %v", err)
	}
	annotations := transformed.(*batchv1.Job).Annotations
	if got := annotations[config.JobAcquisitionTokenSecretAnnotation]; got != "job-token" {
		t.Errorf("job acquisition token secret annotation = %q, want %q", got, "job-token")
	}
	if _, ok := annotations["unneeded"]; ok {
		t.Error("unneeded annotation was preserved")
	}
}
