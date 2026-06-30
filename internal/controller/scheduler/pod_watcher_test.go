package scheduler

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestPodHasExceededPendingTimeout(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	w := &podWatcher{podPendingTimeout: 5 * time.Minute}
	require.NotNil(t, w)

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
		tt := tt
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
					RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
				},
				{Name: "imagecheck-0"},
			},
		},
	}

	assert.False(t, isSidecarInitContainer(pod, "checkout"))
	assert.True(t, isSidecarInitContainer(pod, "sidecar-0"))
	assert.False(t, isSidecarInitContainer(pod, "imagecheck-0"))
	assert.False(t, isSidecarInitContainer(pod, "nonexistent"))
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatImagePullFailureNotification(tt.statuses))
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
	assert.Empty(t, got, "should respect grace period")

	// Past the grace period: the command container's ImagePullBackOff is detected.
	got = w.podHasFailingImages(logger, newRunningPod(time.Minute))
	require.Len(t, got, 1)
	assert.Equal(t, "container-0", got[0].Name)
	assert.Equal(t, "ImagePullBackOff", got[0].State.Waiting.Reason)
}
