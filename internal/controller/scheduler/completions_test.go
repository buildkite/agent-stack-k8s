package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

// TestCompletionsWatcherRegisterInformerReplaysWithValidContext covers the
// completions watcher's half of
// https://github.com/buildkite/agent-stack-k8s/issues/951. It creates the Pods
// informer, but the factory may already have been started by the deduper and
// limiter for the Jobs informer, in which case the Pods informer is started
// under the same factory and cached Pods replay while RegisterInformer is
// still running. See TestJobWatcherRegisterInformerReplaysWithValidContext for
// the mechanism.
func TestCompletionsWatcherRegisterInformerReplaysWithValidContext(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	k8sClient := fake.NewSimpleClientset()
	w := NewPodCompletionWatcher(logger, k8sClient, 30)

	kjob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "buildkite-job", Namespace: "default"}}
	if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create job: %v", err)
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "buildkite-pod",
			Namespace: "default",
			Labels:    map[string]string{config.UUIDLabel: testJobUUID, "job-name": kjob.Name},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  AgentContainerName,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
			}},
		},
	}
	if _, err := k8sClient.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}

	factory := startedAndSyncedFactory(t, ctx, k8sClient, func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
		return f.Core().V1().Pods().Informer()
	})

	if err := w.RegisterInformer(ctx, factory); err != nil {
		t.Fatalf("RegisterInformer: %v", err)
	}

	// cleanupSidecars sets ActiveDeadlineSeconds on the Job, so this is only
	// true once OnAdd has run and reached the Kubernetes API with a usable
	// context.
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, replayTimeout, true, func(ctx context.Context) (bool, error) {
		got, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
		return err == nil && got.Spec.ActiveDeadlineSeconds != nil, nil
	}); err != nil {
		t.Fatalf("Job ActiveDeadlineSeconds not set during informer replay: %v", err)
	}
}
