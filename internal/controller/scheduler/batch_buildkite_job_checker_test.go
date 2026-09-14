package scheduler

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/google/uuid"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCancelCheckerMissingResource(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind cancelTargetKind
	}{
		{name: "pod", kind: cancelTargetPod},
		{name: "job", kind: cancelTargetJob},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			client := fake.NewSimpleClientset()
			checker := NewBatchBuildkiteJobChecker(slog.New(slog.NewTextHandler(&logs, nil)), nil, client, time.Second)
			id := uuid.New()
			target := cancelTarget{kind: tc.kind, meta: metav1.ObjectMeta{Name: "already-deleted", Namespace: "default"}}
			checker.checkingJobs[id] = target

			// A delete event can be delayed or lost; NotFound must also end
			// cancellation checks rather than requeueing the same DELETE forever.
			checker.handleJobState(t.Context(), id.String(), api.JobStateCanceled, target)

			if got := checker.GetActiveCheckCount(); got != 0 {
				t.Fatalf("active checks = %d, want 0 after NotFound", got)
			}
			if strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("already-deleted resource logged as an error: %s", &logs)
			}
		})
	}
}

func TestCancelCheckerRetainsFailedDeletion(t *testing.T) {
	for _, resource := range []string{"pods", "jobs"} {
		for _, deleteErr := range []error{
			kerrors.NewForbidden(schema.GroupResource{Resource: resource}, "pending", errors.New("denied")),
			kerrors.NewInternalError(errors.New("temporary failure")),
		} {
			t.Run(resource+"/"+string(kerrors.ReasonForError(deleteErr)), func(t *testing.T) {
				client := fake.NewSimpleClientset()
				client.PrependReactor("delete", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, deleteErr
				})
				checker := NewBatchBuildkiteJobChecker(slog.Default(), nil, client, time.Second)
				id := uuid.New()
				target := cancelTarget{kind: cancelTargetPod, meta: metav1.ObjectMeta{Name: "pending", Namespace: "default"}}
				if resource == "jobs" {
					target.kind = cancelTargetJob
				}
				checker.checkingJobs[id] = target

				checker.handleJobState(t.Context(), id.String(), api.JobStateCanceling, target)

				if got := checker.GetActiveCheckCount(); got != 1 {
					t.Errorf("active checks = %d, want 1 so failed cleanup is retried", got)
				}
			})
		}
	}
}

func TestCancelCheckerWaitsForSlowDeletion(t *testing.T) {
	server := api.NewFakeAgentServer()
	defer server.Close()
	id := uuid.MustParse(testJobUUID)
	server.JobStates = map[string]string{id.String(): string(api.JobStateCanceled)}
	_, client, checker := newTestJobWatcherWithChecker(t, server)
	checker.AddK8sJob(id, metav1.ObjectMeta{Name: "pending", Namespace: "default"})

	started := make(chan struct{})
	release := make(chan struct{})
	client.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		close(started)
		<-release
		return true, nil, nil
	})
	finished := make(chan struct{})
	go func() {
		checker.checkJobStates(t.Context())
		close(finished)
	}()
	defer func() {
		close(release)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("poll did not finish after cancellation DELETE completed")
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation DELETE did not start")
	}
	// A slow API call must apply backpressure to the polling loop. Returning
	// early lets each tick enqueue another DELETE behind the same rate limiter.
	select {
	case <-finished:
		t.Fatal("poll completed while cancellation DELETE was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
}
