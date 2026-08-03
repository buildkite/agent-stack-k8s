package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/uuid"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

const testJobUUID = "019f719c-0000-0000-0000-000000000000"

func newTestJobWatcher(t *testing.T, fakeServer *api.FakeAgentServer) (*jobWatcher, *fake.Clientset) {
	t.Helper()
	w, k8sClient, _ := newTestJobWatcherWithChecker(t, fakeServer)
	return w, k8sClient
}

func newTestJobWatcherWithChecker(t *testing.T, fakeServer *api.FakeAgentServer) (*jobWatcher, *fake.Clientset, *BatchBuildkiteJobChecker) {
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

	checker := NewBatchBuildkiteJobChecker(logger, agentClient, k8sClient, time.Second)

	w := NewJobWatcher(logger, k8sClient, agentClient, &config.Config{
		Namespace:           "default",
		EmptyJobGracePeriod: 30 * time.Second,
	}, checker)

	return w, k8sClient, checker
}

func newTestK8sJob(jobUUID string) *batchv1.Job {
	// Started long enough ago to be past EmptyJobGracePeriod.
	started := metav1.NewTime(time.Now().Add(-time.Minute))
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "buildkite-job-" + jobUUID[:8],
			Namespace: "default",
			UID:       "original-uid",
			Labels: map[string]string{
				config.UUIDLabel: jobUUID,
			},
		},
		Status: batchv1.JobStatus{
			StartTime: &started,
		},
	}
}

func TestCleanupStalledJobs(t *testing.T) {
	tests := []struct {
		name string
		// bkState is the job state reported by the fake BK API. Empty means
		// the job is unknown to BK (deleted/expired).
		bkState          string
		getJobStateFails bool
		finishJobFails   bool
		// livePods makes the k8s Job in the fake clientset have an active
		// pod, while the snapshot in stallingJobs remains podless (a stale
		// informer cache).
		livePods bool
		// liveTerminating is like livePods, but the pod is terminating:
		// excluded from Active and not yet in Failed/Succeeded/UTP.
		liveTerminating bool
		// jobMissing leaves the k8s Job out of the fake clientset entirely.
		jobMissing bool
		// jobReplaced gives the k8s Job in the fake clientset a different
		// UID, as if the candidate was deleted and recreated under the same
		// deterministic name.
		jobReplaced bool
		// getK8sJobFails makes live Gets of the k8s Job fail.
		getK8sJobFails bool

		wantFinishJobCalls int
		wantCleanedUp      bool
		wantStalling       bool
		wantIgnored        bool
	}{
		{
			name:          "skips cleanup when an agent is working on the job",
			bkState:       "running",
			wantCleanedUp: false,
			wantStalling:  false,
			wantIgnored:   false,
		},
		{
			name:          "skips cleanup when an agent has accepted the job",
			bkState:       "accepted",
			wantCleanedUp: false,
			wantStalling:  false,
			wantIgnored:   false,
		},
		{
			name:               "cleans up when BK job is reserved",
			bkState:            "reserved",
			wantFinishJobCalls: 1,
			wantCleanedUp:      true,
			wantStalling:       false,
			wantIgnored:        true,
		},
		{
			name:               "cleans up when BK job is scheduled",
			bkState:            "scheduled",
			wantFinishJobCalls: 1,
			wantCleanedUp:      true,
			wantStalling:       false,
			wantIgnored:        true,
		},
		{
			name:               "cleans up when BK job has ended",
			bkState:            "finished",
			wantFinishJobCalls: 1,
			wantCleanedUp:      true,
			wantStalling:       false,
			wantIgnored:        true,
		},
		{
			name:               "cleans up when BK job is unknown (deleted/expired)",
			bkState:            "",
			wantFinishJobCalls: 1,
			wantCleanedUp:      true,
			wantStalling:       false,
			wantIgnored:        true,
		},
		{
			name:             "keeps job in stalling set when GetJobState fails, for retry next cycle",
			getJobStateFails: true,
			wantCleanedUp:    false,
			wantStalling:     true,
			wantIgnored:      false,
		},
		{
			name:          "skips cleanup when the live API shows pods (stale informer cache)",
			bkState:       "scheduled",
			livePods:      true,
			wantCleanedUp: false,
			wantStalling:  false,
			wantIgnored:   false,
		},
		{
			// A terminating pod is a pod: the job controller replaces it,
			// so the job is not stalled.
			name:            "skips cleanup when the live API shows only a terminating pod",
			bkState:         "scheduled",
			liveTerminating: true,
			wantCleanedUp:   false,
			wantStalling:    false,
			wantIgnored:     false,
		},
		{
			name:           "keeps job in stalling set when the live k8s Get fails, for retry next cycle",
			bkState:        "scheduled",
			getK8sJobFails: true,
			wantStalling:   true,
			wantIgnored:    false,
		},
		{
			// Nothing to reap, and the BK job may be legitimately recreated
			// once the deduper drops the UUID.
			name:         "skips cleanup when the k8s Job no longer exists",
			bkState:      "scheduled",
			jobMissing:   true,
			wantStalling: false,
			wantIgnored:  false,
		},
		{
			// The candidate was deleted and legitimately recreated under the
			// same deterministic name; the fresh replacement must not be
			// reaped with the old candidate's expired grace period.
			name:          "skips cleanup when the k8s Job was replaced (delete/recreate race)",
			bkState:       "scheduled",
			jobReplaced:   true,
			wantCleanedUp: false,
			wantStalling:  false,
			wantIgnored:   false,
		},
		{
			name:               "cleans up even when failJob fails",
			bkState:            "reserved",
			finishJobFails:     true,
			wantFinishJobCalls: 1,
			wantCleanedUp:      true,
			wantStalling:       false,
			wantIgnored:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			server := api.NewFakeAgentServer()
			defer server.Close()

			if tt.bkState != "" {
				server.JobStates = map[string]string{testJobUUID: tt.bkState}
			}
			if tt.getJobStateFails {
				server.GetJobStatesStatusCode = 500
				server.GetJobStatesError = "internal error"
			}
			if tt.finishJobFails {
				server.FinishJobStatusCode = 404
				server.FinishJobError = "not found"
			}

			w, k8sClient := newTestJobWatcher(t, server)

			// The snapshot in stallingJobs is always podless; the copy in
			// the fake clientset represents the live API state.
			kjob := newTestK8sJob(testJobUUID)
			if !tt.jobMissing {
				live := kjob.DeepCopy()
				if tt.livePods {
					live.Status.Active = 1
				}
				if tt.liveTerminating {
					live.Status.Terminating = ptr.To[int32](1)
				}
				if tt.jobReplaced {
					live.UID = "replacement-uid"
				}
				if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, live, metav1.CreateOptions{}); err != nil {
					t.Fatalf("Create job: %v", err)
				}
			}
			if tt.getK8sJobFails {
				k8sClient.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("api server unavailable")
				})
			}

			jobUUID := uuid.MustParse(testJobUUID)
			w.addToStalling(jobUUID, kjob)

			w.cleanupStalledJobs(ctx)

			if got := len(server.FinishJobCalls); got != tt.wantFinishJobCalls {
				t.Errorf("len(FinishJobCalls) = %d, want %d", got, tt.wantFinishJobCalls)
			}

			if !tt.jobMissing && !tt.getK8sJobFails {
				updated, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("Get job: %v", err)
				}
				cleanedUp := updated.Spec.ActiveDeadlineSeconds != nil && *updated.Spec.ActiveDeadlineSeconds == 1
				if cleanedUp != tt.wantCleanedUp {
					t.Errorf("job cleaned up (ActiveDeadlineSeconds set) = %t, want %t", cleanedUp, tt.wantCleanedUp)
				}
			}

			w.stallingJobsMu.Lock()
			_, stalling := w.stallingJobs[jobUUID]
			w.stallingJobsMu.Unlock()
			if stalling != tt.wantStalling {
				t.Errorf("job in stallingJobs = %t, want %t", stalling, tt.wantStalling)
			}

			if got := w.isIgnored(jobUUID); got != tt.wantIgnored {
				t.Errorf("isIgnored = %t, want %t", got, tt.wantIgnored)
			}
		})
	}
}

// While confirmStalled checks a candidate against the APIs, an informer event
// may replace the stallingJobs entry with a recreated Job (same name, new
// UID). The skip must not remove the replacement's entry, or a genuinely
// stalled replacement would never be checked again.
func TestCleanupStalledJobs_ReplacementAddedDuringCheck(t *testing.T) {
	ctx := context.Background()
	server := api.NewFakeAgentServer()
	defer server.Close()
	server.JobStates = map[string]string{testJobUUID: "scheduled"}

	w, k8sClient := newTestJobWatcher(t, server)

	candidate := newTestK8sJob(testJobUUID)
	replacement := newTestK8sJob(testJobUUID)
	replacement.UID = "replacement-uid"
	if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, replacement, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create job: %v", err)
	}

	jobUUID := uuid.MustParse(testJobUUID)
	w.addToStalling(jobUUID, candidate)

	// Simulate the replacement's OnAdd arriving while confirmStalled waits
	// on the live Get, then fall through to the default tracker.
	k8sClient.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		w.addToStalling(jobUUID, replacement)
		return false, nil, nil
	})

	w.cleanupStalledJobs(ctx)

	w.stallingJobsMu.Lock()
	current := w.stallingJobs[jobUUID]
	w.stallingJobsMu.Unlock()
	if current == nil || current.UID != replacement.UID {
		t.Errorf("stallingJobs[jobUUID] = %v, want the replacement to survive the skip", current)
	}
	if got := len(server.FinishJobCalls); got != 0 {
		t.Errorf("len(FinishJobCalls) = %d, want 0", got)
	}
	if w.isIgnored(jobUUID) {
		t.Error("isIgnored = true, want false")
	}
}

// A k8s Job that has no pod must still be registered for Buildkite cancellation
// checks. Before this, registration happened only on pod events, so a Job held
// suspended by an admission controller such as Kueue was never checked at all:
// cancelling the build did nothing, and the job ran in full whenever it was
// later admitted.
func TestPodlessJobIsRegisteredForCancelChecks(t *testing.T) {
	t.Parallel()

	fakeServer := api.NewFakeAgentServer()
	defer fakeServer.Close()

	w, _, checker := newTestJobWatcherWithChecker(t, fakeServer)

	kjob := newTestK8sJob(testJobUUID)
	kjob.Spec.Suspend = ptr.To(true)

	w.runChecks(context.Background(), kjob)

	if got := checker.GetActiveCheckCount(); got != 1 {
		t.Fatalf("checker.GetActiveCheckCount() = %d, want 1", got)
	}

	jobUUID := uuid.MustParse(testJobUUID)
	target, ok := checker.targetFor(jobUUID)
	if !ok {
		t.Fatalf("checker has no target for %s", testJobUUID)
	}
	if target.kind != cancelTargetJob {
		t.Errorf("target.kind = %v, want cancelTargetJob", target.kind)
	}
	if target.meta.Name != kjob.Name {
		t.Errorf("target.meta.Name = %q, want %q", target.meta.Name, kjob.Name)
	}
}

// Once a pod exists it is the better thing to delete, and a later Job event
// must not downgrade the registration back to the Job.
func TestPodTargetIsNotDowngradedByJobEvent(t *testing.T) {
	t.Parallel()

	fakeServer := api.NewFakeAgentServer()
	defer fakeServer.Close()

	w, _, checker := newTestJobWatcherWithChecker(t, fakeServer)

	jobUUID := uuid.MustParse(testJobUUID)
	checker.AddPod(jobUUID, metav1.ObjectMeta{Name: "the-pod", Namespace: "default"})

	kjob := newTestK8sJob(testJobUUID)
	w.runChecks(context.Background(), kjob)

	target, ok := checker.targetFor(jobUUID)
	if !ok {
		t.Fatalf("checker has no target for %s", testJobUUID)
	}
	if target.kind != cancelTargetPod {
		t.Errorf("target.kind = %v, want cancelTargetPod", target.kind)
	}
	if target.meta.Name != "the-pod" {
		t.Errorf("target.meta.Name = %q, want %q", target.meta.Name, "the-pod")
	}
}

// A finished Job must be deregistered, so the checker does not keep polling
// Buildkite for jobs that are already over.
func TestFinishedJobIsDeregistered(t *testing.T) {
	t.Parallel()

	fakeServer := api.NewFakeAgentServer()
	defer fakeServer.Close()

	w, _, checker := newTestJobWatcherWithChecker(t, fakeServer)

	jobUUID := uuid.MustParse(testJobUUID)
	checker.AddK8sJob(jobUUID, metav1.ObjectMeta{Name: "the-job", Namespace: "default"})

	kjob := newTestK8sJob(testJobUUID)
	kjob.Status.Succeeded = 1
	kjob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}

	w.runChecks(context.Background(), kjob)

	if got := checker.GetActiveCheckCount(); got != 0 {
		t.Errorf("checker.GetActiveCheckCount() = %d, want 0", got)
	}
}
