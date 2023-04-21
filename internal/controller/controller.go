package controller

import (
	"context"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

func Run(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface, cfg api.Config) {
	if cfg.ProfilerAddress != "" {
		logger.Info("profiler listening for requests")
		go func() {
			srv := http.Server{Addr: cfg.ProfilerAddress, ReadHeaderTimeout: 2 * time.Second}
			if err := srv.ListenAndServe(); err != nil {
				logger.Error("problem running profiler server", zap.Error(err))
			}
		}()
	}

	m, err := monitor.New(logger.Named("monitor"), k8sClient, cfg)
	if err != nil {
		logger.Fatal("failed to create monitor", zap.Error(err))
	}

	sched := scheduler.New(logger.Named("scheduler"), k8sClient, cfg)
	limiter := scheduler.NewLimiter(logger.Named("limiter"), sched, cfg.MaxInFlight)

	informerFactory, err := scheduler.NewInformerFactory(k8sClient, cfg.Tags)
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
