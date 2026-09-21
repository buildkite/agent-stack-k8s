package scheduler

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

// newTestAgentClient returns an AgentClient pointed at fakeServer, along with
// the logger it was built with.
func newTestAgentClient(t *testing.T, fakeServer *api.FakeAgentServer) (*api.AgentClient, *slog.Logger) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	agentClient, err := api.NewAgentClient(t.Context(), api.AgentClientOpts{
		Token:    "fake-token",
		Endpoint: fakeServer.URL(),
		StackID:  "test-stack",
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewAgentClient: %v", err)
	}
	return agentClient, logger
}

// startedAndSyncedFactory returns a SharedInformerFactory that has already been
// started and synced for the informer chosen by pick. That is the started and
// synced state a watcher's RegisterInformer finds the factory in when another
// component (the deduper, the limiter, or the completions watcher) registered
// on it first.
func startedAndSyncedFactory(t *testing.T, ctx context.Context, k8sClient *fake.Clientset, pick func(informers.SharedInformerFactory) cache.SharedIndexInformer) informers.SharedInformerFactory {
	t.Helper()
	factory := informers.NewSharedInformerFactoryWithOptions(k8sClient, 0, informers.WithNamespace("default"))
	informer := pick(factory)
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		t.Fatal("failed to sync informer cache")
	}
	return factory
}
