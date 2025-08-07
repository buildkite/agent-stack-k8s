package scheduler

import (
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

// applyResourceClass applies resource class if specified in agent tags as "resource_class"
func (w *worker) applyResourceClass(podSpec *corev1.PodSpec, tags map[string]string) error {
	resourceClassName, resourceClassTagExist := tags["resource_class"]
	if !resourceClassTagExist {
		return nil
	}

	if w.cfg.ResourceClasses == nil {
		return fmt.Errorf("resource classes not configured but resource_class tag specified")
	}
	resourceClass, resourceClassFound := w.cfg.ResourceClasses[resourceClassName]

	if !resourceClassFound {
		return fmt.Errorf("resource class not found: %s", resourceClassName)
	}

	resourceClass.Apply(podSpec)
	w.logger.Debug("Applied resource class", zap.String("resource_class", resourceClassName))

	return nil
}
