package scheduler

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// applyResourceClass applies resource class if specified in agent tags as "resource_class",
// or applies the default resource class if configured and no tag is present.
func (w *worker) applyResourceClass(podSpec *corev1.PodSpec, tags map[string]string) error {
	resourceClassName, resourceClassTagExist := tags["resource_class"]

	if !resourceClassTagExist {
		if w.cfg.DefaultResourceClassName == "" {
			return nil
		}
		resourceClassName = w.cfg.DefaultResourceClassName
	}

	if w.cfg.ResourceClasses == nil {
		if resourceClassTagExist {
			return fmt.Errorf("resource classes not configured but resource_class tag specified")
		}
		return fmt.Errorf("resource classes not configured but default-resource-class-name is set")
	}

	resourceClass, resourceClassFound := w.cfg.ResourceClasses[resourceClassName]
	if !resourceClassFound {
		if resourceClassTagExist {
			return fmt.Errorf("resource class not found: %s", resourceClassName)
		}
		return fmt.Errorf("default resource class not found: %s", resourceClassName)
	}

	resourceClass.Apply(podSpec)
	if resourceClassTagExist {
		w.logger.Debug("Applied resource class", "resource_class", resourceClassName)
	} else {
		w.logger.Debug("Applied default resource class", "resource_class", resourceClassName)
	}

	return nil
}
