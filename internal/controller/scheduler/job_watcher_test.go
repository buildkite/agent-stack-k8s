package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const testJobUUID = "019f719c-0000-0000-0000-000000000000"

func newTestJobWatcher(t *testing.T, fakeServer *api.FakeAgentServer) (*jobWatcher, *fake.Clientset) {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	agentClient, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: fakeServer.URL(),
		StackID:  "test-stack",
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewAgentClient: %v", err)
	}

	k8sClient := fake.NewSimpleClientset()

	w := NewJobWatcher(logger, k8sClient, agentClient, &config.Config{
		Namespace:           "default",
		EmptyJobGracePeriod: 30 * time.Second,
	})

	return w, k8sClient
}

func newTestK8sJob(jobUUID string) *batchv1.Job {
	now := metav1.Now()
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "buildkite-job-" + jobUUID[:8],
			Namespace: "default",
			Labels: map[string]string{
				config.UUIDLabel: jobUUID,
			},
		},
		Status: batchv1.JobStatus{
			StartTime: &now,
		},
	}
}

func TestCleanupStalledJob(t *testing.T) {
	t.Parallel()

	t.Run("skips cleanup when BK job is running", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.JobStates = map[string]string{testJobUUID: "running"}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		// Create the job in the fake K8s client so we can check it after
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// Should NOT have called FinishJob
		if got := len(server.FinishJobCalls); got != 0 {
			t.Errorf("FinishJobCalls = %d, want 0", got)
		}

		// Should NOT have set ActiveDeadlineSeconds
		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds != nil {
			t.Errorf("ActiveDeadlineSeconds = %d, want nil", *updated.Spec.ActiveDeadlineSeconds)
		}
	})

	t.Run("skips cleanup when BK job is accepted", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.JobStates = map[string]string{testJobUUID: "accepted"}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		if got := len(server.FinishJobCalls); got != 0 {
			t.Errorf("FinishJobCalls = %d, want 0", got)
		}

		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds != nil {
			t.Errorf("ActiveDeadlineSeconds = %d, want nil", *updated.Spec.ActiveDeadlineSeconds)
		}
	})

	t.Run("proceeds with cleanup when BK job is reserved", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.JobStates = map[string]string{testJobUUID: "reserved"}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// Should have called FinishJob
		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
		} else if server.FinishJobCalls[0] != testJobUUID {
			t.Errorf("FinishJobCalls[0] = %q, want %q", server.FinishJobCalls[0], testJobUUID)
		}

		// Should have set ActiveDeadlineSeconds = 1
		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds == nil {
			t.Fatal("ActiveDeadlineSeconds = nil, want 1")
		}
		if got := *updated.Spec.ActiveDeadlineSeconds; got != 1 {
			t.Errorf("ActiveDeadlineSeconds = %d, want 1", got)
		}
	})

	t.Run("proceeds with cleanup when BK job is scheduled", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.JobStates = map[string]string{testJobUUID: "scheduled"}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
		}

		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds == nil {
			t.Fatal("ActiveDeadlineSeconds = nil, want 1")
		}
		if got := *updated.Spec.ActiveDeadlineSeconds; got != 1 {
			t.Errorf("ActiveDeadlineSeconds = %d, want 1", got)
		}
	})

	t.Run("skips cleanup when GetJobState fails", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.GetJobStatesStatusCode = 500
		server.GetJobStatesError = "internal error"
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		if got := len(server.FinishJobCalls); got != 0 {
			t.Errorf("FinishJobCalls = %d, want 0", got)
		}

		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds != nil {
			t.Errorf("ActiveDeadlineSeconds = %d, want nil", *updated.Spec.ActiveDeadlineSeconds)
		}
	})

	t.Run("still patches ActiveDeadlineSeconds when failJob fails", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		// State is reserved (so GetJobState guard passes), but FinishJob returns 404
		server.JobStates = map[string]string{testJobUUID: "reserved"}
		server.FinishJobStatusCode = 404
		server.FinishJobError = "not found"
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// FinishJob WAS called (but it failed)
		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
		}

		// Should STILL set ActiveDeadlineSeconds — GetJobState already confirmed
		// no agent is running, so the pod is safe to clean up regardless of
		// whether failJob succeeded.
		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds == nil {
			t.Fatal("ActiveDeadlineSeconds = nil, want 1")
		}
		if got := *updated.Spec.ActiveDeadlineSeconds; got != 1 {
			t.Errorf("ActiveDeadlineSeconds = %d, want 1", got)
		}
	})
}
