package scheduler

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const handleTestJobUUID = "01a184a3-58b6-48ba-aab5-66821324bd3c"

func TestHandleRetriesNotFoundFromGetJob(t *testing.T) {
	t.Parallel()

	server := api.NewFakeAgentServer()
	defer server.Close()
	server.AgentJobs = map[string]*api.AgentJob{
		handleTestJobUUID: {ID: handleTestJobUUID, Command: "true"},
	}
	// The Stacks API can briefly return 404 for a job this stack has just reserved.
	server.GetJobErrorStatusCodes = []int{http.StatusNotFound}

	k8sClient := fake.NewSimpleClientset()
	worker := newHandleTestWorker(t, server, k8sClient)

	if err := worker.Handle(t.Context(), &api.AgentScheduledJob{ID: handleTestJobUUID}); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}

	wantCalls := []string{handleTestJobUUID, handleTestJobUUID}
	if diff := cmp.Diff(server.GetJobCalls, wantCalls); diff != "" {
		t.Errorf("server.GetJobCalls diff (-got +want):\n%s", diff)
	}
	jobs, err := k8sClient.BatchV1().Jobs("default").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List(jobs) error = %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Errorf("len(jobs.Items) = %d, want 1", len(jobs.Items))
	}
}

func TestHandleDoesNotRetryOtherPermanentErrorsFromGetJob(t *testing.T) {
	t.Parallel()

	server := api.NewFakeAgentServer()
	defer server.Close()
	server.AgentJobs = map[string]*api.AgentJob{
		handleTestJobUUID: {ID: handleTestJobUUID, Command: "true"},
	}
	server.GetJobErrorStatusCodes = []int{http.StatusUnprocessableEntity}

	k8sClient := fake.NewSimpleClientset()
	worker := newHandleTestWorker(t, server, k8sClient)

	if err := worker.Handle(t.Context(), &api.AgentScheduledJob{ID: handleTestJobUUID}); err == nil {
		t.Fatal("Handle() error = nil, want error")
	}

	wantCalls := []string{handleTestJobUUID}
	if diff := cmp.Diff(server.GetJobCalls, wantCalls); diff != "" {
		t.Errorf("server.GetJobCalls diff (-got +want):\n%s", diff)
	}
	jobs, err := k8sClient.BatchV1().Jobs("default").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List(jobs) error = %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Errorf("len(jobs.Items) = %d, want 0", len(jobs.Items))
	}
}

func newHandleTestWorker(t *testing.T, server *api.FakeAgentServer, k8sClient *fake.Clientset) *worker {
	t.Helper()

	agentClient, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: server.URL(),
		StackID:  "test-stack",
		Logger:   slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	return New(slog.Default(), k8sClient, agentClient, Config{
		Namespace:            "default",
		Image:                "buildkite/agent:latest",
		AgentTokenSecretName: "token-secret",
	})
}
