package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/reserver"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

const agentTokenKey = "BUILDKITE_AGENT_TOKEN"

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

	// Agent token required to query for jobs.
	agentToken, err := fetchAgentToken(ctx, logger, k8sClient, cfg.Namespace, cfg.AgentTokenSecret)
	if err != nil {
		logger.Error("Couldn't get agent token from secret", zap.Error(err))
		return
	}

	agentEndpoint := ""
	if cfg.AgentConfig != nil && cfg.AgentConfig.Endpoint != nil {
		agentEndpoint = *cfg.AgentConfig.Endpoint
	}

	agentTags, tagErrs := agenttags.TagMapFromTags(cfg.Tags)
	if err := errors.Join(tagErrs...); err != nil {
		logger.Error("Couldn't process the configured agent tags", zap.Error(err))
		return
	}

	queue := agentTags["queue"]
	if queue == "" {
		logger.Info("Listening to the default queue for the given cluster. To listen to a specific queue, please pass in 'queue' as a tag, e.g. --tags='queue=kubernetes'")
	}

	agentTokenClient, err := api.NewAgentTokenClient(agentToken, agentEndpoint)
	if err != nil {
		logger.Error("Couldn't create Agent token API client", zap.Error(err))
		return
	}
	agentTokenIdentity, _, err := agentTokenClient.GetTokenIdentity(ctx)
	if err != nil {
		logger.Error("Couldn't get agent token identity", zap.Error(err))
		return
	}

	if agentTokenIdentity.ClusterUUID == "" {
		logger.Error("Detected unclustered agent token, please upgrade to a cluster agent token: https://buildkite.com/organizations/~/clusters/~/tokens")
		return
	}

	logger.Info("Starting controller",
		zap.String("cluster-name", agentTokenIdentity.ClusterName),
		zap.String("organization-slug", agentTokenIdentity.OrganizationSlug),
		zap.String("agent-endpoint", agentEndpoint),
		zap.String("namespace", cfg.Namespace),
		zap.String("queue", queue),
		zap.String("stack-id", cfg.ID),
	)

	agentClient, err := api.NewAgentClient(ctx, api.AgentClientOpts{
		Token:           agentToken,
		Endpoint:        agentEndpoint,
		ClusterID:       agentTokenIdentity.ClusterUUID,
		Queue:           queue,
		StackID:         cfg.ID,
		AgentQueryRules: cfg.Tags,
		Logger:          logger,
		UseStacksAPI:    cfg.ExperimentalStacksAPISupport,
	})
	if err != nil {
		logger.Error("Couldn't create Agent API client", zap.Error(err))
		return
	}

	// **************************************************************************
	// ***                        JOB FLOW                                    ***
	// ***       Monitor -> Reserver -> Limiter -> Deduper -> Scheduler       ***
	// **************************************************************************
	//
	// Monitor polls Buildkite for jobs. It passes them to Limiter.
	m, err := monitor.New(logger.Named("monitor"), agentClient, monitor.Config{
		Namespace:            cfg.Namespace,
		ClusterUUID:          agentTokenIdentity.ClusterUUID,
		Queue:                queue,
		MaxInFlight:          cfg.MaxInFlight,
		PollInterval:         cfg.PollInterval,
		Tags:                 cfg.Tags,
		TagMap:               agentTags,
		PaginationPageSize:   cfg.PaginationPageSize,
		PaginationDepthLimit: cfg.PaginationDepthLimit,
	})
	if err != nil {
		logger.Fatal("failed to create monitor", zap.Error(err))
	}

	// Scheduler does the complicated work of converting a Buildkite job into
	// a pod to run that job. It talks to the k8s API to create pods.
	sched := scheduler.New(logger.Named("scheduler"), k8sClient, agentClient, scheduler.Config{
		Namespace:                            cfg.Namespace,
		ID:                                   cfg.ID,
		Image:                                cfg.Image,
		AgentTokenSecretName:                 cfg.AgentTokenSecret,
		JobTTL:                               cfg.JobTTL,
		JobPrefix:                            cfg.JobPrefix,
		JobActiveDeadlineSeconds:             cfg.JobActiveDeadlineSeconds,
		DefaultTerminationGracePeriodSeconds: cfg.DefaultTerminationGracePeriodSeconds,
		AdditionalRedactedVars:               cfg.AdditionalRedactedVars,
		WorkspaceVolume:                      cfg.WorkspaceVolume,
		AgentConfig:                          cfg.AgentConfig,
		DefaultCheckoutParams:                cfg.DefaultCheckoutParams,
		DefaultCommandParams:                 cfg.DefaultCommandParams,
		DefaultSidecarParams:                 cfg.DefaultSidecarParams,
		DefaultMetadata:                      cfg.DefaultMetadata,
		DefaultImagePullPolicy:               cfg.DefaultImagePullPolicy,
		DefaultImageCheckPullPolicy:          cfg.DefaultImageCheckPullPolicy,
		PodSpecPatch:                         cfg.PodSpecPatch,
		ProhibitK8sPlugin:                    cfg.ProhibitKubernetesPlugin,
		AllowPodSpecPatchUnsafeCmdMod:        cfg.AllowPodSpecPatchUnsafeCmdMod,
		SkipImageCheckContainers:             cfg.SkipImageCheckContainers,
		ImageCheckContainerCPULimit:          cfg.ImageCheckContainerCPULimit,
		ImageCheckContainerMemoryLimit:       cfg.ImageCheckContainerMemoryLimit,
		ResourceClasses:                      cfg.ResourceClasses,
	})

	informerFactory, err := NewInformerFactory(k8sClient, cfg.Namespace, cfg.ID)
	if err != nil {
		logger.Fatal("failed to create informer", zap.Error(err))
	}

	// Deduper prevents multiple pods being scheduled for the same job.
	// It passes jobs to the final scheduler.
	deduper := deduper.New(logger.Named("deduper"), sched)
	if err := deduper.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register deduper informer", zap.Error(err))
	}

	// Limiter prevents scheduling more than cfg.MaxInFlight jobs at once
	// (if configured) and is responsible for the priority queue of jobs.
	// Once it figures out a job can be scheduled, it passes to the deduper.
	limiter := limiter.New(ctx, logger.Named("limiter"), deduper,
		cfg.MaxInFlight,
		cfg.JobCreationConcurrency,
		cfg.WorkQueueLimit,
	)
	if err := limiter.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register limiter informer", zap.Error(err))
	}

	// Reserver marks the job as reserved in backend if possible.
	// Then pases jobs to the limiter.
	//
	// This happens right after monitor so we can maximize our collected signals.
	// But it does bring a trade-off of more likely reservation expiration.
	reserver := reserver.New(logger.Named("reserver"), agentClient,
		limiter,
		cfg.ExperimentalStacksAPISupport,
	)

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
		agentClient,
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
		agentClient,
		cfg,
	)
	if err := podWatcher.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register podWatcher informer", zap.Error(err))
	}
	podWatcher.StartBuildkiteJobChecker(ctx)

	select {
	case <-ctx.Done():
		logger.Info("gracefully shutting down controller...")

		// The DeregisterStack call is a no-op if stacks API support is disabled, but we don't want to log "deregistered stack"
		// unless we actually did it.
		if cfg.ExperimentalStacksAPISupport {
			ctx = context.WithoutCancel(ctx) // we want stack deregistration to happen, even though the context has been cancelled
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			// Try best-effort to deregister the stack, but if it fails or times out, the backend will still clean it up.
			if err := agentClient.DeregisterStack(ctx); err != nil {
				logger.Error("failed to deregister stack", zap.Error(err))
				return
			}

			logger.Info("deregistered stack")
		}

		logger.Info("controller exiting", zap.Error(ctx.Err()))

	case err := <-m.Start(ctx, reserver):
		logger.Info("monitor failed", zap.Error(err))
	}
}

// NewInformerFactory returns an informer factory configured to watch resources
// (pods, jobs) created by the scheduler. It matches pods that are labeled with
// a job uuid and the agent tags that the scheduler was configured with.
func NewInformerFactory(
	k8s kubernetes.Interface,
	namespace string,
	id string,
) (informers.SharedInformerFactory, error) {
	requirements := make(labels.Requirements, 0)
	hasUUID, err := labels.NewRequirement(config.UUIDLabel, selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build uuid label selector for job manager: %w", err)
	}
	requirements = append(requirements, *hasUUID)

	if id != "" {
		hasRightID, err := labels.NewRequirement(config.ControllerIDLabel, selection.Equals, []string{id})
		if err != nil {
			return nil, fmt.Errorf("failed to build controller id label selector for job manager: %w", err)
		}
		requirements = append(requirements, *hasRightID)
	} else {
		// In the case when ID isn't specified, we shouldn't try to monitor those jobs that has ID label.
		// Note that ID is set by the Helm chart, so this should not happen in usual installations.
		noID, err := labels.NewRequirement(config.ControllerIDLabel, selection.DoesNotExist, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to build controller id label selector for job manager: %w", err)
		}
		requirements = append(requirements, *noID)
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

// fetchAgentToken fetches the agent token from the agent token secret.
func fetchAgentToken(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface, namespace, agentTokenSecretName string) (string, error) {
	// Need to fetch the agent token ourselves.
	tokenSecret, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, agentTokenSecretName, metav1.GetOptions{})
	if err != nil {
		logger.Error("fetching agent token from secret", zap.Error(err))
		return "", err
	}
	agentToken := string(tokenSecret.Data[agentTokenKey])
	if agentToken == "" {
		logger.Error("agent token is empty")
		return "", errors.New("agent token is empty")
	}
	return agentToken, nil
}
