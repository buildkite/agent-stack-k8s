package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/uuid"

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


	t.Run("skips cleanup when BK job is running", func(t *testing.T) {
	
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

	t.Run("still patches ActiveDeadlineSeconds when failJob fails but recheck confirms reserved", func(t *testing.T) {
	
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		// State is reserved (so both initial and recheck pass), but FinishJob returns 404
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

		// Recheck confirmed still reserved, so ActiveDeadlineSeconds should be set.
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

	t.Run("unignores job when GetJobState fails so retry is possible", func(t *testing.T) {

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

		jobUUID := uuid.MustParse(testJobUUID)
		w.ignoreJob(jobUUID) // simulate what stalledJobChecker does before calling cleanupStalledJob

		w.cleanupStalledJob(ctx, kjob)

		// Job should be unignored so it can be retried
		if w.isIgnored(jobUUID) {
			t.Error("job is still ignored after GetJobState failure; should have been unignored for retry")
		}
	})

	t.Run("unignores job when recheck GetJobState fails so retry is possible", func(t *testing.T) {

		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		// Initial state check succeeds (reserved), failJob fails, recheck fails
		server.JobStates = map[string]string{testJobUUID: "reserved"}
		server.FinishJobStatusCode = 404
		server.FinishJobError = "not found"
		server.OnFinishJob = func(jobUUID string) {
			server.GetJobStatesError = "internal error"
			server.GetJobStatesStatusCode = 500
		}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		jobUUID := uuid.MustParse(testJobUUID)
		w.ignoreJob(jobUUID)

		w.cleanupStalledJob(ctx, kjob)

		// Job should be unignored so it can be retried
		if w.isIgnored(jobUUID) {
			t.Error("job is still ignored after recheck GetJobState failure; should have been unignored for retry")
		}
	})

	t.Run("aborts cleanup when agent acquires job between state check and failJob", func(t *testing.T) {
	
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		// Initial state check returns "reserved", but by the time we recheck
		// after failJob fails, the agent has acquired the job ("running").
		// We simulate this by having the server change state after FinishJob is called.
		server.JobStates = map[string]string{testJobUUID: "reserved"}
		server.FinishJobStatusCode = 404
		server.FinishJobError = "not found"
		server.OnFinishJob = func(jobUUID string) {
			server.JobStates[testJobUUID] = "running"
		}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// FinishJob was called (and failed)
		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
		}

		// Recheck saw "running" — should NOT have set ActiveDeadlineSeconds
		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds != nil {
			t.Errorf("ActiveDeadlineSeconds = %d, want nil (agent acquired job during cleanup)", *updated.Spec.ActiveDeadlineSeconds)
		}
	})

	t.Run("proceeds with cleanup when BK job state is empty (job deleted/expired)", func(t *testing.T) {
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		// JobStates is empty — the test UUID is absent, so GetJobState returns ""
		server.JobStates = map[string]string{}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// Should have called FinishJob (even though it will likely fail on BK side)
		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
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

	t.Run("aborts cleanup when recheck after failJob error also fails", func(t *testing.T) {
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.JobStates = map[string]string{testJobUUID: "reserved"}
		server.FinishJobStatusCode = 404
		server.FinishJobError = "not found"
		// After FinishJob is called, make GetJobStates also fail
		server.OnFinishJob = func(jobUUID string) {
			server.GetJobStatesError = "internal error"
			server.GetJobStatesStatusCode = 500
		}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// FinishJob was called (and failed)
		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
		}

		// Recheck also failed — should NOT have set ActiveDeadlineSeconds
		updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if updated.Spec.ActiveDeadlineSeconds != nil {
			t.Errorf("ActiveDeadlineSeconds = %d, want nil (recheck failed, should abort)", *updated.Spec.ActiveDeadlineSeconds)
		}
	})

	t.Run("proceeds with cleanup when recheck returns empty state after failJob error", func(t *testing.T) {
		ctx := context.Background()
		server := api.NewFakeAgentServer()
		defer server.Close()

		server.JobStates = map[string]string{testJobUUID: "reserved"}
		server.FinishJobStatusCode = 404
		server.FinishJobError = "not found"
		// After FinishJob is called, the BK job disappears from the API
		server.OnFinishJob = func(jobUUID string) {
			delete(server.JobStates, testJobUUID)
		}
		w, k8sClient := newTestJobWatcher(t, server)

		kjob := newTestK8sJob(testJobUUID)
		if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Create job: %v", err)
		}

		w.cleanupStalledJob(ctx, kjob)

		// FinishJob was called (and failed)
		if got := len(server.FinishJobCalls); got != 1 {
			t.Errorf("len(FinishJobCalls) = %d, want 1", got)
		}

		// Recheck returned empty — should still proceed with ActiveDeadlineSeconds
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
