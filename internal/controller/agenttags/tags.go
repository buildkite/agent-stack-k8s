package agenttags

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ToMap converts a slice of strings of the form `k=v` to a map where the
// key is `k` and the value is `v`. If any element of the slice does not
// have that form, it will not be inserted into the map and instead generate
// an error which will be appended to the second return value.
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
		namespacedKey := "tag.buildkite.com/" + k
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

// ToLabels converts a slice of strings of the form `k=v` to a map where the
// key is `k` and the value is `v`. If any element of the slice does not
// have that form or if `k` is not a valid kubernetes label name or if `v`
// is not a valid kubernetes label value, it will not be inserted into the
// map and instead generate an error which will be appended to the second
// return value.
//
// See https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
func ToLabels(tags []string) (map[string]string, []error) {
	m, errs1 := ToMap(tags)
	labels, errs2 := mapToLabels(m)
	return labels, append(errs1, errs2...)
}

// JobTagsMatchAgentTags returns true if and only if, for each tag key in
// `jobTags`: either the tag key is also present in `agentTags`, and the tag
// value in `jobTags` is "*" or the same as the tag value in `agentTags`
//
// In the future, this may be expanded to: if the tag value `agentTags` is in some
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
