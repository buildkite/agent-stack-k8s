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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestPodHasExceededPendingTimeout(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	w := &podWatcher{podPendingTimeout: 5 * time.Minute}

	now := time.Now()

	newPod := func(phase corev1.PodPhase, createdAt metav1.Time) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-pod",
				Namespace:         "default",
				CreationTimestamp: createdAt,
			},
			Status: corev1.PodStatus{Phase: phase},
		}
	}

	tests := []struct {
		name string
		pod  func(time.Time) *corev1.Pod
		want bool
	}{
		{
			name: "not pending running pod",
			pod: func(now time.Time) *corev1.Pod {
				return newPod(corev1.PodRunning, metav1.NewTime(now.Add(-10*time.Minute)))
			},
			want: false,
		},
		{
			name: "not pending succeeded pod",
			pod: func(now time.Time) *corev1.Pod {
				return newPod(corev1.PodSucceeded, metav1.NewTime(now.Add(-10*time.Minute)))
			},
			want: false,
		},
		{
			name: "pending within timeout",
			pod: func(now time.Time) *corev1.Pod {
				return newPod(corev1.PodPending, metav1.NewTime(now.Add(-1*time.Minute)))
			},
			want: false,
		},
		{
			name: "pending exceeded timeout",
			pod: func(now time.Time) *corev1.Pod {
				return newPod(corev1.PodPending, metav1.NewTime(now.Add(-10*time.Minute)))
			},
			want: true,
		},
		{
			name: "pending with image pull backoff init container",
			pod: func(now time.Time) *corev1.Pod {
				pod := newPod(corev1.PodPending, metav1.NewTime(now.Add(-10*time.Minute)))
				pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
					Name: "imagecheck-0",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
					},
				}}
				return pod
			},
			want: false,
		},
		{
			name: "pending with err image never pull init container",
			pod: func(now time.Time) *corev1.Pod {
				pod := newPod(corev1.PodPending, metav1.NewTime(now.Add(-10*time.Minute)))
				pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
					Name: "imagecheck-0",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImageNeverPull"},
					},
				}}
				return pod
			},
			want: false,
		},
		{
			name: "pending with invalid image name container",
			pod: func(now time.Time) *corev1.Pod {
				pod := newPod(corev1.PodPending, metav1.NewTime(now.Add(-10*time.Minute)))
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "container-0",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "InvalidImageName"},
					},
				}}
				return pod
			},
			want: false,
		},
		{
			name: "pending with non-image waiting reason",
			pod: func(now time.Time) *corev1.Pod {
				pod := newPod(corev1.PodPending, metav1.NewTime(now.Add(-10*time.Minute)))
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "container-0",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
					},
				}}
				return pod
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pod := tt.pod(now)
			got := w.podHasExceededPendingTimeout(logger, pod)

			if got != tt.want {
				t.Errorf("w.podHasExceededPendingTimeout(logger, %v) = %v, want %v", pod, got, tt.want)
			}
		})
	}
}

func TestIsSidecarInitContainer(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "checkout"},
				{
					Name:          "sidecar-0",
					RestartPolicy: new(corev1.ContainerRestartPolicyAlways),
				},
				{Name: "imagecheck-0"},
			},
		},
	}

	if got := isSidecarInitContainer(pod, "checkout"); got {
		t.Errorf("isSidecarInitContainer(pod, \"checkout\") = %t, want false", got)
	}
	if got := isSidecarInitContainer(pod, "sidecar-0"); !got {
		t.Errorf("isSidecarInitContainer(pod, \"sidecar-0\") = %t, want true", got)
	}
	if got := isSidecarInitContainer(pod, "imagecheck-0"); got {
		t.Errorf("isSidecarInitContainer(pod, \"imagecheck-0\") = %t, want false", got)
	}
	if got := isSidecarInitContainer(pod, "nonexistent"); got {
		t.Errorf("isSidecarInitContainer(pod, \"nonexistent\") = %t, want false", got)
	}
}

func TestFormatImagePullFailureNotification(t *testing.T) {
	t.Parallel()

	waiting := func(name, reason, message string) corev1.ContainerStatus {
		return corev1.ContainerStatus{
			Name: name,
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message},
			},
		}
	}

	tests := []struct {
		name     string
		statuses []corev1.ContainerStatus
		want     string
	}{
		{
			name:     "single container with message",
			statuses: []corev1.ContainerStatus{waiting("container-0", "ImagePullBackOff", `Back-off pulling image "nope:latest"`)},
			want:     "Image pull failure; the job will fail once the agent times out. container-0: ImagePullBackOff (Back-off pulling image \"nope:latest\")",
		},
		{
			name:     "reason without message",
			statuses: []corev1.ContainerStatus{waiting("container-0", "ErrImageNeverPull", "")},
			want:     "Image pull failure; the job will fail once the agent times out. container-0: ErrImageNeverPull",
		},
		{
			name: "multiple containers sorted by name",
			statuses: []corev1.ContainerStatus{
				waiting("container-1", "ErrImagePull", "pull failed"),
				waiting("container-0", "ImagePullBackOff", "backing off"),
			},
			want: "Image pull failure; the job will fail once the agent times out. container-0: ImagePullBackOff (backing off); container-1: ErrImagePull (pull failed)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, want := formatImagePullFailureNotification(tt.statuses), tt.want; got != want {
				t.Errorf("formatImagePullFailureNotification(tt.statuses) = %q, want %q", got, want)
			}
		})
	}
}

// TestPodHasFailingImages_RunningPod confirms the detection logic itself works
// on a Running pod whose command container is in ImagePullBackOff (the scenario
// in SUP-6708). A failure here would mean Option A's notification can never
// fire, since failForImageFailure is only reached when this returns non-empty.
func TestPodHasFailingImages_RunningPod(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	w := &podWatcher{imagePullBackOffGracePeriod: 30 * time.Second}

	newRunningPod := func(startedAgo time.Duration) *corev1.Pod {
		start := metav1.NewTime(time.Now().Add(-startedAgo))
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
			Status: corev1.PodStatus{
				Phase:     corev1.PodRunning,
				StartTime: &start,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "container-0",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ImagePullBackOff",
							Message: `Back-off pulling image "nope:latest"`,
						},
					},
				}},
			},
		}
	}

	// Within the grace period: not failing yet.
	got := w.podHasFailingImages(logger, newRunningPod(5*time.Second))
	if len(got) != 0 {
		t.Errorf("should respect grace period")
	}

	// Past the grace period: the command container's ImagePullBackOff is detected.
	got = w.podHasFailingImages(logger, newRunningPod(time.Minute))
	if got, want := len(got), 1; got != want {
		t.Fatalf("len(w.podHasFailingImages(logger, newRunningPod(time.Minute))) = %d, want %d", got, want)
	}
	if got, want := got[0].Name, "container-0"; got != want {
		t.Errorf("got[0].Name = %q, want %q", got, want)
	}
	if got, want := got[0].State.Waiting.Reason, "ImagePullBackOff"; got != want {
		t.Errorf("got[0].State.Waiting.Reason = %q, want %q", got, want)
	}
}

func newTestPodWatcher(t *testing.T, fakeServer *api.FakeAgentServer) (*podWatcher, *fake.Clientset) {
	t.Helper()
	agentClient, logger := newTestAgentClient(t, fakeServer)
	k8sClient := fake.NewSimpleClientset()
	checker := NewBatchBuildkiteJobChecker(logger, agentClient, k8sClient, time.Second)
	w := NewPodWatcher(logger, k8sClient, agentClient, &config.Config{Namespace: "default"}, checker)
	return w, k8sClient
}

// TestPodWatcherRegisterInformerReplaysWithValidContext covers podWatcher's
// half of https://github.com/buildkite/agent-stack-k8s/issues/951. When the
// completions watcher is enabled it creates and starts the Pods informer
// first, so podWatcher registers on an already-started informer and its
// cached Pods replay while RegisterInformer is still running. See
// TestJobWatcherRegisterInformerReplaysWithValidContext for the mechanism.
func TestPodWatcherRegisterInformerReplaysWithValidContext(t *testing.T) {
	// Deliberately not t.Parallel(): NewPodWatcher writes package-level gauge
	// funcs, which parallel tests in this package already race on.
	fakeServer := api.NewFakeAgentServer()
	defer fakeServer.Close()
	// Cancel before fakeServer.Close (defers run LIFO), so the watcher's
	// goroutines stop before the server they talk to goes away.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, k8sClient := newTestPodWatcher(t, fakeServer)

	jobUUID := uuid.MustParse(testJobUUID)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "buildkite-pod",
			Namespace: "default",
			Labels:    map[string]string{config.UUIDLabel: testJobUUID},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
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

	// runChecks on a pending Pod registers it for image-failure watching, so
	// this is only true once OnAdd has run.
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, replayTimeout, true, func(context.Context) (bool, error) {
		w.watchingForImageFailureMu.Lock()
		defer w.watchingForImageFailureMu.Unlock()
		_, ok := w.watchingForImageFailure[jobUUID]
		return ok, nil
	}); err != nil {
		t.Fatalf("pending Pod not registered for image failure watching during informer replay: %v", err)
	}
}
