package scheduler

import (
	"context"
	"errors"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FailureInfo contains data about a job failure for reporting to Buildkite.
type FailureInfo struct {
	Message  string
	ExitCode int32
	Reason   string
}

// failForK8sObject figures out how to fail the BK job corresponding to
// the k8s object (a pod or job) by inspecting the object's labels.
func failForK8sObject(ctx context.Context, logger *zap.Logger, obj metav1.Object, failureInfo FailureInfo, agentClient *api.AgentClient) error {
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

	return agentClient.FailJob(ctx, jobUUID, failureInfo.Message)
}
