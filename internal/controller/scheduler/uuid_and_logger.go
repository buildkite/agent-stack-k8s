package scheduler

import (
	"errors"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/uuid"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// loggerForObject curries a logger with namespace, name, and job UUID taken
// from the object labels.
func loggerForObject(baseLog *zap.Logger, obj metav1.Object) *zap.Logger {
	return baseLog.With(
		zap.String("namespace", obj.GetNamespace()),
		zap.String("name", obj.GetName()),
		zap.String("jobUUID", obj.GetLabels()[config.UUIDLabel]),
	)
}

// jobUUIDForObject parses the Buildkite job UUID from the object labels.
func jobUUIDForObject(obj metav1.Object) (uuid.UUID, error) {
	rawJobUUID := obj.GetLabels()[config.UUIDLabel]
	if rawJobUUID == "" {
		return uuid.UUID{}, errors.New("no job UUID label")
	}

	return uuid.Parse(rawJobUUID)
}
