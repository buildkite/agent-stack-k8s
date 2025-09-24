package scheduler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/version"

	"github.com/buildkite/agent/v3/agent"
	agentcore "github.com/buildkite/agent/v3/core"
	"github.com/buildkite/agent/v3/logger"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// FailureInfo contains data about a job failure for reporting to Buildkite.
type FailureInfo struct {
	Message  string
	ExitCode int32
	Reason   string
}

// failForK8sObject figures out how to fail the BK job corresponding to
// the k8s object (a pod or job) by inspecting the object's labels.
func failForK8sObject(
	ctx context.Context,
	logger *zap.Logger,
	obj metav1.Object,
	failureInfo FailureInfo,

	// This is used for API driven fail job.
	agentClient *api.AgentClient,

	// These two are used by fail job via acquire and fail.
	k8sClient kubernetes.Interface,
	cfg *config.Config,
) error {

	logger.Info(
		"failing a job for k8s object",
		zap.String("name", obj.GetName()),
	)

	// Matching tags are required order to connect the temporary agent.
	labels := obj.GetLabels()
	jobUUID := labels[config.UUIDLabel]
	if jobUUID == "" {
		logger.Error("object missing UUID label", zap.String("label", config.UUIDLabel))
		return errors.New("missing UUID label")
	}
	if agentClient.UseStackAPI() {
		return agentClient.FailJob(ctx, jobUUID, failureInfo.Message)
	} else {
		tags := agenttags.TagsFromLabels(labels)
		opts := cfg.AgentConfig.ControllerOptions()
		agentToken, err := fetchAgentToken(ctx, logger, k8sClient, obj.GetNamespace(), cfg.AgentTokenSecret)
		if err != nil {
			logger.Error("fetching agent token from secret", zap.Error(err))
			return err
		}
		return acquireAndFail(ctx, logger, agentToken, cfg.JobPrefix, jobUUID, tags, failureInfo, opts...)
	}
}

// acquireAndFail fails the job in Buildkite. agentToken needs to be the token value.
// Use fetchAgentToken to fetch it from the k8s secret.
func acquireAndFail(
	ctx context.Context,
	zapLogger *zap.Logger,
	agentToken string,
	jobPrefix string,
	jobUUID string,
	tags []string,
	failureInfo FailureInfo,
	options ...agentcore.ControllerOption,
) error {
	opts := append([]agentcore.ControllerOption{
		agentcore.WithUserAgent("agent-stack-k8s/" + version.Version()),
		agentcore.WithLogger(logger.NewConsoleLogger(logger.NewTextPrinter(os.Stderr), func(int) {})),
	}, options...)

	// queue is required for acquire! maybe more
	ctr, err := agentcore.NewController(ctx, agentToken, k8sJobName(jobPrefix, jobUUID), tags, opts...)
	if err != nil {
		zapLogger.Error("registering or connecting ephemeral agent", zap.Error(err))
		return fmt.Errorf("registering or connecting ephemeral agent: %w", err)
	}
	defer ctr.Close(ctx)

	job, err := ctr.AcquireJob(ctx, jobUUID)
	if err != nil {
		zapLogger.Error("acquiring job", zap.Error(err))
		return fmt.Errorf("acquiring job: %w", err)
	}

	jctr := ctr.NewJobController(job)
	if err := jctr.Start(ctx); err != nil {
		zapLogger.Error("starting job", zap.Error(err))
		return fmt.Errorf("starting job: %w", err)
	}

	if err := jctr.WriteLog(ctx, failureInfo.Message); err != nil {
		zapLogger.Error("writing log", zap.Error(err))
		return fmt.Errorf("writing log: %w", err)
	}

	var ignoreAgentInDispatches *bool

	failureInfo.ExitCode = cmp.Or(failureInfo.ExitCode, 1)
	// By default, we consider all failure triggered by the stack controller are stack errors
	failureInfo.Reason = cmp.Or(failureInfo.Reason, string(agent.SignalReasonStackError))

	processExit := agentcore.ProcessExit{Status: int(failureInfo.ExitCode), SignalReason: failureInfo.Reason}
	if err := jctr.Finish(ctx, processExit, ignoreAgentInDispatches); err != nil {
		zapLogger.Error("finishing job", zap.Error(err))
		return fmt.Errorf("finishing job: %w", err)
	}

	return nil
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
