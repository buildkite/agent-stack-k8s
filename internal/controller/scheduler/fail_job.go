package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"

	agentcore "github.com/buildkite/agent/v3/core"
	"github.com/buildkite/agent/v3/logger"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// failJob fails the job in Buildkite. agentToken needs to be the token value.
// Use fetchAgentToken to fetch it from the k8s secret.
func failJob(
	ctx context.Context,
	zapLogger *zap.Logger,
	agentToken string,
	jobUUID string,
	tags []string,
	message string,
	options ...agentcore.ControllerOption,
) error {
	opts := append([]agentcore.ControllerOption{
		agentcore.WithUserAgent("agent-stack-k8s/" + version.Version()),
		agentcore.WithLogger(logger.NewConsoleLogger(logger.NewTextPrinter(os.Stderr), func(int) {})),
	}, options...)

	// queue is required for acquire! maybe more
	ctr, err := agentcore.NewController(ctx, agentToken, kjobName(jobUUID), tags, opts...)
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

	if err := jctr.WriteLog(ctx, message); err != nil {
		zapLogger.Error("writing log", zap.Error(err))
		return fmt.Errorf("writing log: %w", err)
	}

	if err := jctr.Finish(ctx, agentcore.ProcessExit{Status: 1}); err != nil {
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
