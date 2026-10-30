package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	"github.com/prometheus/client_golang/prometheus/testutil"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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
	pod := agentTerminatedPod(kjob.Name, time.Now(), runningContainer("unmanaged-sidecar"))
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

func TestSidecarCleanupDeadline(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 34, 9, 0, time.UTC)
	startedAgo := func(d time.Duration) *metav1.Time {
		return &metav1.Time{Time: now.Add(-d)}
	}

	tests := []struct {
		name            string
		grace           int64
		startTime       *metav1.Time
		currentDeadline *int64
		want            int64
	}{
		{
			name:  "no start time falls back to the grace period",
			grace: 60,
			want:  60,
		},
		{
			// Containers still get the rest of the grace period from the Job's
			// start, as before.
			name:      "job shorter than the grace period keeps the grace period from its start",
			grace:     60,
			startTime: startedAgo(20 * time.Second),
			want:      60,
		},
		{
			// The case from #999: the Job started 76s before the agent exited.
			// A deadline of 60 would already be in the past.
			name:      "job longer than the grace period gets the buffer after the agent exits",
			grace:     60,
			startTime: startedAgo(76 * time.Second),
			want:      86,
		},
		{
			name:      "buffer is capped at a shorter grace period",
			grace:     5,
			startTime: startedAgo(76 * time.Second),
			want:      81,
		},
		{
			name:      "rounds partial seconds up",
			grace:     60,
			startTime: startedAgo(70*time.Second + 300*time.Millisecond),
			want:      81,
		},
		{
			// Clock skew: the agent exited well before the Job's recorded start.
			// elapsed+buffer is then below the grace period, so the grace
			// period is kept. This is far enough before the start that
			// |elapsed|+buffer would exceed the grace period, unlike the case
			// below, so it also catches elapsed being treated as positive.
			name:      "agent exit well before the job's start keeps the grace period",
			grace:     60,
			startTime: startedAgo(-2 * time.Minute),
			want:      60,
		},
		{
			name:      "start time in the future is treated as just started",
			grace:     60,
			startTime: startedAgo(-5 * time.Second),
			want:      60,
		},
		{
			name:            "keeps a sooner configured deadline",
			grace:           60,
			startTime:       startedAgo(time.Hour),
			currentDeadline: new(int64(3605)),
			want:            3605,
		},
		{
			name:            "replaces a later configured deadline",
			grace:           60,
			startTime:       startedAgo(76 * time.Second),
			currentDeadline: new(int64(21600)),
			want:            86,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &batchv1.Job{
				Spec:   batchv1.JobSpec{ActiveDeadlineSeconds: tt.currentDeadline},
				Status: batchv1.JobStatus{StartTime: tt.startTime},
			}
			if got := sidecarCleanupDeadline(job, tt.grace, now); got != tt.want {
				t.Errorf("sidecarCleanupDeadline() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCompletionsWatcherDeadline runs cleanupSidecars against a Job and checks
// the deadline it writes, if any.
func TestCompletionsWatcherDeadline(t *testing.T) {
	start := metav1.NewTime(time.Now().Add(-10 * time.Minute).Truncate(time.Second))
	agentExit := start.Add(5 * time.Minute)
	finishedCommand := corev1.ContainerStatus{
		Name:  "container-0",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
	}
	unmanaged := runningContainer("unmanaged-sidecar")
	waiting := corev1.ContainerStatus{
		Name:  "unmanaged-sidecar",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
	}
	nativeSidecar := runningContainer("sidecar-0")

	tests := []struct {
		name            string
		grace           int
		agentExit       time.Time
		others          []corev1.ContainerStatus
		initStatuses    []corev1.ContainerStatus
		currentDeadline *int64
		want            *int64 // nil: no deadline written
		wantAtLeast     int64  // for a deadline that depends on the real clock
		wantNoUpdate    bool
		wantCleanups    float64 // change in the cleanups counter
	}{
		{
			// #999: the Job has been running longer than the buffer, so the
			// deadline must be measured from the agent exiting.
			name:         "unmanaged container still running gets the buffer after the agent exits",
			grace:        60,
			agentExit:    agentExit,
			others:       []corev1.ContainerStatus{finishedCommand, unmanaged},
			want:         new(int64(310)),
			wantCleanups: 1,
		},
		{
			// A job that finished well within the grace period keeps the
			// deadline it always had: the grace period from the Job's start.
			// An unmanaged container that exits on its own within that time
			// lets the Job complete.
			name:         "job shorter than the grace period keeps the grace period from its start",
			grace:        60,
			agentExit:    start.Add(20 * time.Second),
			others:       []corev1.ContainerStatus{finishedCommand, unmanaged},
			want:         new(int64(60)),
			wantCleanups: 1,
		},
		{
			name:         "buffer is capped at a shorter grace period",
			grace:        5,
			agentExit:    agentExit,
			others:       []corev1.ContainerStatus{unmanaged},
			want:         new(int64(305)),
			wantCleanups: 1,
		},
		{
			// An unmanaged container that never started still keeps the pod
			// from finishing, so it must be cleaned up too.
			name:         "container still waiting to start gets a deadline",
			grace:        60,
			agentExit:    agentExit,
			others:       []corev1.ContainerStatus{finishedCommand, waiting},
			want:         new(int64(310)),
			wantCleanups: 1,
		},
		{
			name:            "keeps a sooner configured deadline",
			grace:           60,
			agentExit:       agentExit,
			others:          []corev1.ContainerStatus{unmanaged},
			currentDeadline: new(int64(200)),
			want:            new(int64(200)),
			wantNoUpdate:    true,
		},
		{
			// A controller restart replays the pod through OnAdd after the
			// deadline was already set. The replay computes the same deadline
			// from the agent's FinishedAt, so the Job isn't written again.
			name:            "replay does not rewrite the deadline",
			grace:           60,
			agentExit:       agentExit,
			others:          []corev1.ContainerStatus{unmanaged},
			currentDeadline: new(int64(310)),
			want:            new(int64(310)),
			wantNoUpdate:    true,
		},
		{
			// Without FinishedAt, measure from now: never from the Job's start,
			// which would bring back #999.
			name:         "zero FinishedAt measures from now",
			grace:        60,
			others:       []corev1.ContainerStatus{unmanaged},
			wantAtLeast:  int64(time.Since(start.Time).Seconds()) + 10,
			wantCleanups: 1,
		},
		{
			// #999: nothing left to clean up, so the pod finishes on its own.
			// A running native sidecar is an init container, which the kubelet
			// stops itself, so it doesn't count.
			name:         "no deadline when every other regular container has terminated",
			grace:        60,
			agentExit:    agentExit,
			others:       []corev1.ContainerStatus{finishedCommand},
			initStatuses: []corev1.ContainerStatus{nativeSidecar},
			wantNoUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			k8sClient := fake.NewSimpleClientset()
			w := NewPodCompletionWatcher(logger, k8sClient, tt.grace)

			kjob := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "buildkite-job", Namespace: "default"},
				Spec:       batchv1.JobSpec{ActiveDeadlineSeconds: tt.currentDeadline},
				Status:     batchv1.JobStatus{StartTime: &start},
			}
			if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
				t.Fatalf("Create job: %v", err)
			}

			pod := agentTerminatedPod(kjob.Name, tt.agentExit, tt.others...)
			pod.Status.InitContainerStatuses = tt.initStatuses
			cleanupsBefore := testutil.ToFloat64(completionWatcherJobCleanupsCounter)

			w.cleanupSidecars(ctx, pod)

			if got := testutil.ToFloat64(completionWatcherJobCleanupsCounter) - cleanupsBefore; got != tt.wantCleanups {
				t.Errorf("cleanups counter increased by %v, want %v", got, tt.wantCleanups)
			}

			got, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Get job: %v", err)
			}
			gotDeadline := got.Spec.ActiveDeadlineSeconds
			switch {
			case tt.wantAtLeast > 0:
				if gotDeadline == nil || *gotDeadline < tt.wantAtLeast {
					t.Errorf("ActiveDeadlineSeconds = %v, want at least %d", optionalSeconds(gotDeadline), tt.wantAtLeast)
				}
			case tt.want == nil && gotDeadline != nil:
				t.Errorf("ActiveDeadlineSeconds = %d, want unset", *gotDeadline)
			case tt.want != nil && gotDeadline == nil:
				t.Errorf("ActiveDeadlineSeconds unset, want %d", *tt.want)
			case tt.want != nil && *gotDeadline != *tt.want:
				t.Errorf("ActiveDeadlineSeconds = %d, want %d", *gotDeadline, *tt.want)
			}

			if tt.wantNoUpdate {
				for _, action := range k8sClient.Actions() {
					if action.GetVerb() == "update" {
						t.Errorf("cleanupSidecars updated the Job, want no update")
					}
				}
			}
		})
	}
}

// TestCompletionsWatcherRetriesOnConflict covers the RetryOnConflict path: the
// first Update is rejected with a conflict, and the retry re-reads the Job
// before deciding whether to write.
func TestCompletionsWatcherRetriesOnConflict(t *testing.T) {
	start := metav1.NewTime(time.Now().Add(-10 * time.Minute).Truncate(time.Second))
	agentExit := start.Add(5 * time.Minute)

	tests := []struct {
		name string
		// concurrentDeadline, if set, is written to the Job by another writer
		// before the conflict is returned.
		concurrentDeadline *int64
		wantUpdates        int
		wantCleanups       float64
	}{
		{
			name:         "retry writes the deadline",
			wantUpdates:  2,
			wantCleanups: 1,
		},
		{
			// The deadline is already what this watcher would write, so the
			// retry must not write it again or count it as a cleanup.
			name:               "retry finds the deadline already written",
			concurrentDeadline: new(int64(310)),
			wantUpdates:        1,
			wantCleanups:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			k8sClient := fake.NewSimpleClientset()
			w := NewPodCompletionWatcher(logger, k8sClient, 60)

			kjob := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "buildkite-job", Namespace: "default"},
				Status:     batchv1.JobStatus{StartTime: &start},
			}
			if _, err := k8sClient.BatchV1().Jobs("default").Create(ctx, kjob, metav1.CreateOptions{}); err != nil {
				t.Fatalf("Create job: %v", err)
			}

			conflicted := false
			k8sClient.PrependReactor("update", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
				if conflicted {
					return false, nil, nil
				}
				conflicted = true
				if tt.concurrentDeadline != nil {
					job := kjob.DeepCopy()
					job.Spec.ActiveDeadlineSeconds = tt.concurrentDeadline
					if err := k8sClient.Tracker().Update(batchv1.SchemeGroupVersion.WithResource("jobs"), job, job.Namespace); err != nil {
						t.Fatalf("Simulate concurrent update: %v", err)
					}
				}
				return true, nil, kerrors.NewConflict(batchv1.Resource("jobs"), kjob.Name, errors.New("conflict"))
			})
			cleanupsBefore := testutil.ToFloat64(completionWatcherJobCleanupsCounter)

			w.cleanupSidecars(ctx, agentTerminatedPod(kjob.Name, agentExit, runningContainer("unmanaged-sidecar")))

			got, err := k8sClient.BatchV1().Jobs("default").Get(ctx, kjob.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Get job: %v", err)
			}
			if d := got.Spec.ActiveDeadlineSeconds; d == nil || *d != 310 {
				t.Errorf("ActiveDeadlineSeconds = %v, want 310", optionalSeconds(d))
			}
			updates := 0
			for _, action := range k8sClient.Actions() {
				if action.GetVerb() == "update" {
					updates++
				}
			}
			if updates != tt.wantUpdates {
				t.Errorf("Job updates = %d, want %d", updates, tt.wantUpdates)
			}
			if got := testutil.ToFloat64(completionWatcherJobCleanupsCounter) - cleanupsBefore; got != tt.wantCleanups {
				t.Errorf("cleanups counter increased by %v, want %v", got, tt.wantCleanups)
			}
		})
	}
}

// agentTerminatedPod returns a running pod of the named Job whose agent
// container exited at agentExit (zero for an unknown time), alongside the given
// other container statuses.
func agentTerminatedPod(jobName string, agentExit time.Time, others ...corev1.ContainerStatus) *corev1.Pod {
	agent := corev1.ContainerStatus{
		Name: AgentContainerName,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode:   0,
			FinishedAt: metav1.Time{Time: agentExit},
		}},
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "buildkite-pod",
			Namespace: "default",
			Labels:    map[string]string{config.UUIDLabel: testJobUUID, "job-name": jobName},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: append([]corev1.ContainerStatus{agent}, others...),
		},
	}
}

// runningContainer returns the status of a running container with the given name.
func runningContainer(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}
}
