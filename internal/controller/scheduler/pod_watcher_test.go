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
			name: "pending with zero creation timestamp",
			pod: func(time.Time) *corev1.Pod {
				return newPod(corev1.PodPending, metav1.Time{})
			},
			want: false,
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

			assert.Equal(t, tt.want, got)
		})
	}
}
