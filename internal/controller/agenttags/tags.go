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

// JobTagsMatchAgentTags returns true if and only if, for each tag key in
// `jobTags`: either the tag key is also present in `agentTags`, and the tag
// value in `jobTags` is "*" or the same as the tag value in `agentTags`
//
// In the future, this may be expaned to if the tag value `agentTags` is in some
// set of strings defined by the tag value in `jobTags` (eg a glob or regex)
// See https://buildkite.com/docs/agent/v3/cli-start#agent-targeting
func JobTagsMatchAgentTags(jobTags, agentTags map[string]string) bool {
	for k, v := range jobTags {
		agentTagValue, exists := agentTags[k]
		if !exists || (v != "*" && v != agentTagValue) {
			return false
		}
	}

	return true
}
