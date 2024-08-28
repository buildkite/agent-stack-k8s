package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

func Run(
	ctx context.Context,
	logger *zap.Logger,
	k8sClient kubernetes.Interface,
	cfg *config.Config,
) {
	if cfg.ProfilerAddress != "" {
		logger.Info("profiler listening for requests")
		go func() {
			srv := http.Server{Addr: cfg.ProfilerAddress, ReadHeaderTimeout: 2 * time.Second}
			if err := srv.ListenAndServe(); err != nil {
				logger.Error("problem running profiler server", zap.Error(err))
			}
		}()
	}

	m, err := monitor.New(logger.Named("monitor"), k8sClient, monitor.Config{
		Namespace:    cfg.Namespace,
		Org:          cfg.Org,
		ClusterUUID:  cfg.ClusterUUID,
		MaxInFlight:  cfg.MaxInFlight,
		PollInterval: cfg.PollInterval,
		Tags:         cfg.Tags,
		Token:        cfg.BuildkiteToken,
	})
	if err != nil {
		logger.Fatal("failed to create monitor", zap.Error(err))
	}

	sched := scheduler.New(logger.Named("scheduler"), k8sClient, scheduler.Config{
		Namespace:              cfg.Namespace,
		Image:                  cfg.Image,
		AgentTokenSecretName:   cfg.AgentTokenSecret,
		JobTTL:                 cfg.JobTTL,
		AdditionalRedactedVars: cfg.AdditionalRedactedVars,
		DefaultCheckoutParams:  cfg.DefaultCheckoutParams,
		DefaultCommandParams:   cfg.DefaultCommandParams,
		DefaultSidecarParams:   cfg.DefaultSidecarParams,
		PodSpecPatch:           cfg.PodSpecPatch,
		ProhibitK8sPlugin:      cfg.ProhibitKubernetesPlugin,
	})
	limiter := scheduler.NewLimiter(logger.Named("limiter"), sched, cfg.MaxInFlight)

	informerFactory, err := NewInformerFactory(k8sClient, cfg.Namespace, cfg.Tags)
	if err != nil {
		logger.Fatal("failed to create informer", zap.Error(err))
	}

	if err := limiter.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register limiter informer", zap.Error(err))
	}

	completions := scheduler.NewPodCompletionWatcher(logger.Named("completions"), k8sClient)
	if err := completions.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register completions informer", zap.Error(err))
	}

	imagePullBackOffWatcher := scheduler.NewImagePullBackOffWatcher(
		logger.Named("imagePullBackoffWatcher"),
		k8sClient,
		cfg,
	)
	if err := imagePullBackOffWatcher.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register imagePullBackoffWatcher informer", zap.Error(err))
	}

	select {
	case <-ctx.Done():
		logger.Info("controller exiting", zap.Error(ctx.Err()))
	case err := <-m.Start(ctx, limiter):
		logger.Info("monitor failed", zap.Error(err))
	}
}

// NewInformerFactory returns an informer factory configured to watch resources
// (pods, jobs) created by the scheduler. It matches pods that are labeled with
// a job uuid and the agent tags that the scheduler was configured with.
func NewInformerFactory(
	k8s kubernetes.Interface,
	namespace string,
	tags []string,
) (informers.SharedInformerFactory, error) {
	labelsFromTags, errs := agenttags.ToLabels(tags)
	if len(errs) != 0 {
		return nil, errors.Join(errs...)
	}

	requirements := make(labels.Requirements, 0, len(labelsFromTags)+1)
	hasUUID, err := labels.NewRequirement(config.UUIDLabel, selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build uuid label selector for job manager: %w", err)
	}
	requirements = append(requirements, *hasUUID)

	for l, v := range labelsFromTags {
		hasLabel, err := labels.NewRequirement(l, selection.Equals, []string{v})
		if err != nil {
			return nil, fmt.Errorf("failed create label selector agent tag: %w", err)
		}
		requirements = append(requirements, *hasLabel)
	}

	return informers.NewSharedInformerFactoryWithOptions(
		k8s,
		0,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(opt *metav1.ListOptions) {
			opt.LabelSelector = labels.NewSelector().Add(requirements...).String()
		}),
	), nil
}
