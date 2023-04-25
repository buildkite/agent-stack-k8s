package agenttags

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

func ToMap(tags []string) (map[string]string, []error) {
	m := map[string]string{}
	errs := []error{}
	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) != 2 {
			errs = append(errs, fmt.Errorf("invalid agent tag: %q", tag))
			continue
		}
		m[parts[0]] = parts[1]
	}
	return m, errs
}

func mapToLabels(m map[string]string) (map[string]string, []error) {
	labels := map[string]string{}
	errs := []error{}
	for k, v := range m {
		namespacedKey := "buildkite.com/" + k
		if errMsgs := validation.IsQualifiedName(namespacedKey); len(errMsgs) > 0 {
			for _, errMsg := range errMsgs {
				errs = append(errs, errors.New(errMsg))
			}
			continue
		}

		if errMsgs := validation.IsValidLabelValue(v); len(errMsgs) > 0 {
			for _, errMsg := range errMsgs {
				errs = append(errs, errors.New(errMsg))
			}
			continue
		}

		labels[namespacedKey] = v
	}
	return labels, errs
}

func ToLabels(tags []string) (map[string]string, []error) {
	m, errs1 := ToMap(tags)
	labels, errs2 := mapToLabels(m)
	return labels, append(errs1, errs2...)
}
