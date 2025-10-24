package scheduler

import (
	"errors"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/google/uuid"
	"log/slog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// loggerForObject curries a logger with namespace, name, and job UUID taken
// from the object labels.
func loggerForObject(baseLog *slog.Logger, obj metav1.Object) *slog.Logger {
	return baseLog.With(
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
		"jobUUID", obj.GetLabels()[config.UUIDLabel],
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
