package scheduler

import (
	"context"
	"os"
	"strings"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	"go.uber.org/zap"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"
	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

const defaultTermGracePeriodSeconds = 60

type completionsWatcher struct {
	logger *zap.Logger
	k8s    kubernetes.Interface

	// This is the context passed to RegisterInformer.
	// It's being stored here (grrrr!) because the k8s ResourceEventHandler
	// interface doesn't have context args. (Working around an interface in a
	// library outside of our control is a carve-out from the usual rule.)
	// The context is needed to ensure goroutines are cleaned up.
	resourceEventHandlerCtx context.Context
}

func NewPodCompletionWatcher(logger *zap.Logger, k8s kubernetes.Interface) *completionsWatcher {
	watcher := &completionsWatcher{
		logger: logger,
		k8s:    k8s,
	}
	return watcher
}

// Creates a Pods informer and registers the handler on it
func (w *completionsWatcher) RegisterInformer(ctx context.Context, factory informers.SharedInformerFactory) error {
	informer := factory.Core().V1().Pods().Informer()
	if _, err := informer.AddEventHandler(w); err != nil {
		return err
	}
	w.resourceEventHandlerCtx = ctx // see note on field
	go factory.Start(ctx.Done())
	return nil
}

// ignored
func (w *completionsWatcher) OnDelete(obj any) {}

// handle pods completed while the controller wasn't running
func (w *completionsWatcher) OnAdd(obj any, isInInitialList bool) {
	completionWatcherOnAddEventCounter.Inc()
	pod := obj.(*v1.Pod)
	w.cleanupSidecars(w.resourceEventHandlerCtx, pod)
}

func (w *completionsWatcher) OnUpdate(old any, new any) {
	completionWatcherOnUpdateEventCounter.Inc()

	oldPod := old.(*v1.Pod)
	if terminated := getTermination(oldPod); terminated != nil {
		// skip subsequent reconciles after we've already handled termination
		return
	}

	newPod := new.(*v1.Pod)
	w.cleanupSidecars(w.resourceEventHandlerCtx, newPod)
}

// cleanupSidecars first checks if the container status of the agent container
// in the pod is Terminated. If so, it sends a SIGTERM to PID 1 of each
// sidecar container identified by the "buildkite.com/sidecar-*" annotations
func (w *completionsWatcher) cleanupSidecars(ctx context.Context, pod *v1.Pod) {
	terminated := getTermination(pod)
	if terminated == nil {
		return
	}
	w.logger.Debug(
		"agent finished",
		zap.String("uuid", pod.Labels[config.UUIDLabel]),
		zap.Int32("exit code", terminated.ExitCode),
	)

	for annotation := range pod.Annotations {
		if !strings.HasPrefix(annotation, "buildkite.com/sidecar-") {
			continue
		}

		req := w.k8s.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Name(pod.Name).
			Namespace(pod.Namespace).
			SubResource("exec").
			VersionedParams(&v1.PodExecOptions{
				Container: pod.Annotations[annotation],
				Command:   strings.Fields("kill 1"),
				Stdin:     true,
				Stdout:    true,
				Stderr:    true,
				TTY:       false,
			}, scheme.ParameterCodec)

		executor, err := remotecommand.NewSPDYExecutor(restconfig.GetConfigOrDie(), "POST", req.URL())
		if err != nil {
			completionWatcherJobCleanupErrorsCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()
			w.logger.Error("failed to create remotecommand executor", zap.Error(err))
			return
		}

		w.logger.Debug(
			"Sending SIGTERM to sidecar...",
			zap.String("pod", pod.Name),
			zap.String("namespace", pod.Namespace),
			zap.String("job-uuid", pod.Labels[config.UUIDLabel]),
			zap.String("sidecar", pod.Annotations[annotation]),
		)

		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Tty:    false,
		})
		if err != nil {
			completionWatcherJobCleanupErrorsCounter.WithLabelValues(string(kerrors.ReasonForError(err))).Inc()
			w.logger.Error("failed to send SIGTERM to sidecar", zap.String("sidecar", pod.Annotations[annotation]), zap.Error(err))
			return
		}
	}
	completionWatcherJobCleanupsCounter.Inc()
}

func getTermination(pod *v1.Pod) *v1.ContainerStateTerminated {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != AgentContainerName {
			continue
		}
		if container.State.Terminated != nil {
			// oldPod is not terminated, but newPod is
			return container.State.Terminated
		}
	}
	return nil
}
