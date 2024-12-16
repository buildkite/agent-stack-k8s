package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"

	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	httpMuxes := make(map[string]*http.ServeMux)

	if cfg.ProfilerAddress != "" {
		logger.Info("profiler listening for requests")
		// Specifically set the mux to DefaultServeMux, because the pprof
		// handlers are registered there.
		httpMuxes[cfg.ProfilerAddress] = http.DefaultServeMux
	}
	if cfg.PrometheusPort > 0 {
		logger.Info("prometheus metrics handler listening for requests")
		addr := ":" + strconv.Itoa(int(cfg.PrometheusPort))
		// If PrometheusAddress == ProfilerAddress, the mux will already be set
		// to DefaultServeMux.
		// If PrometheusAddress != ProfilerAddress, we don't want to
		// expose pprof handlers on this mux, so make a new mux.
		mux := httpMuxes[addr]
		if mux == nil {
			mux = http.NewServeMux()
		}
		mux.Handle("GET /metrics", promhttp.Handler())
		httpMuxes[addr] = mux
	}

	for addr, mux := range httpMuxes {
		go func() {
			svr := &http.Server{
				Addr:              addr,
				ReadHeaderTimeout: 2 * time.Second,
				Handler:           mux,
			}
			logger.Error("http server exited", zap.Error(svr.ListenAndServe()))
		}()
	}

	// Monitor polls Buildkite GraphQL for jobs. It passes them to Deduper.
	// Job flow: monitor -> deduper -> limiter -> scheduler.
	m, err := monitor.New(logger.Named("monitor"), k8sClient, monitor.Config{
		GraphQLEndpoint:        cfg.GraphQLEndpoint,
		Namespace:              cfg.Namespace,
		Org:                    cfg.Org,
		ClusterUUID:            cfg.ClusterUUID,
		MaxInFlight:            cfg.MaxInFlight,
		PollInterval:           cfg.PollInterval,
		StaleJobDataTimeout:    cfg.StaleJobDataTimeout,
		JobCreationConcurrency: cfg.JobCreationConcurrency,
		Tags:                   cfg.Tags,
		Token:                  cfg.BuildkiteToken,
	})
	if err != nil {
		logger.Fatal("failed to create monitor", zap.Error(err))
	}

	// Scheduler does the complicated work of converting a Buildkite job into
	// a pod to run that job. It talks to the k8s API to create pods.
	sched := scheduler.New(logger.Named("scheduler"), k8sClient, scheduler.Config{
		Namespace:                  cfg.Namespace,
		Image:                      cfg.Image,
		AgentTokenSecretName:       cfg.AgentTokenSecret,
		JobTTL:                     cfg.JobTTL,
		AdditionalRedactedVars:     cfg.AdditionalRedactedVars,
		WorkspaceVolume:            cfg.WorkspaceVolume,
		AgentConfig:                cfg.AgentConfig,
		DefaultCheckoutParams:      cfg.DefaultCheckoutParams,
		DefaultCommandParams:       cfg.DefaultCommandParams,
		DefaultSidecarParams:       cfg.DefaultSidecarParams,
		DefaultMetadata:            cfg.DefaultMetadata,
		PodSpecPatch:               cfg.PodSpecPatch,
		ProhibitK8sPlugin:          cfg.ProhibitKubernetesPlugin,
		AllowPodSpecPatchRawCmdMod: cfg.AllowPodSpecPatchRawCmdMod,
	})

	informerFactory, err := NewInformerFactory(k8sClient, cfg.Namespace, cfg.Tags)
	if err != nil {
		logger.Fatal("failed to create informer", zap.Error(err))
	}

	nextHandler := model.JobHandler(sched)
	if cfg.MaxInFlight > 0 {
		// Limiter prevents scheduling more than cfg.MaxInFlight jobs at once
		//    (if configured)
		// Once it figures out a job can be scheduled, it passes to the scheduler.
		limiter := limiter.New(logger.Named("limiter"), sched, cfg.MaxInFlight)
		if err := limiter.RegisterInformer(ctx, informerFactory); err != nil {
			logger.Fatal("failed to register limiter informer", zap.Error(err))
		}
		nextHandler = limiter
	}

	// Deduper prevents multiple pods being scheduled for the same job.
	// It passes jobs to the limiter if there is a limit, or directly to the
	// scheduler if there is no limit.
	deduper := deduper.New(logger.Named("deduper"), nextHandler)
	if err := deduper.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register deduper informer", zap.Error(err))
	}

	// PodCompletionWatcher watches k8s for pods where the agent has terminated,
	// in order to clean up the pod. This is necessary because "sidecars" are
	// not internally managed by buildkite-agent, and would continue running
	// forever, preventing the pod being cleaned up.
	completions := scheduler.NewPodCompletionWatcher(logger.Named("completions"), k8sClient)
	if err := completions.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register completions informer", zap.Error(err))
	}

	// JobWatcher watches for jobs in bad conditions to clean up:
	// * Jobs that fail without ever creating a pod
	// * Jobs that stall forever without ever creating a pod
	jobWatcher := scheduler.NewJobWatcher(
		logger.Named("jobWatcher"),
		k8sClient,
		cfg,
	)
	if err := jobWatcher.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register jobWatcher informer", zap.Error(err))
	}

	// PodWatcher watches for other conditions to clean up pods:
	// * Pods where an init container failed for any reason
	// * Pods where a container is in ImagePullBackOff for too long
	// * Pods that are still pending, but the Buildkite job has been cancelled
	podWatcher := scheduler.NewPodWatcher(
		logger.Named("podWatcher"),
		k8sClient,
		cfg,
	)
	if err := podWatcher.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register podWatcher informer", zap.Error(err))
	}

	select {
	case <-ctx.Done():
		logger.Info("controller exiting", zap.Error(ctx.Err()))
	case err := <-m.Start(ctx, deduper):
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
	labelsFromTags, errs := agenttags.LabelsFromTags(tags)
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
