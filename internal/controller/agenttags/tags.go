package agenttags

import (
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// TagMapFromTags converts a slice of strings of the form `k=v` to a map where the
// key is `k` and the value is `v`. If any element of the slice does not
// have that form, it will not be inserted into the map and instead generate
// an error which will be appended to the second return value.
func TagMapFromTags(tags []string) (map[string]string, []error) {
	m := make(map[string]string, len(tags))
	var errs []error
	for _, tag := range tags {
		k, v, has := strings.Cut(tag, "=")
		if !has {
			errs = append(errs, fmt.Errorf("invalid agent tag: %q", tag))
			continue
		}
		m[k] = v
	}
	return m, errs
}

// SetTag sets a tag in the tags slice. Because the tag might not already exist,
// it returns an updated slice (use like append:
// tags = SetTag(tags, "key", "value")).
func SetTag(tags []string, key, val string) []string {
	i := slices.IndexFunc(tags, func(tag string) bool {
		return strings.HasPrefix(tag, key+"=")
	})
	if i < 0 {
		return append(tags, key+"="+val)
	}
	tags[i] = key + "=" + val
	return tags
}

// labelsFromTagMap converts map[key->value] to map[tag.buildkite.com/key->value],
// with k8s compatibility checks
func labelsFromTagMap(m map[string]string) (map[string]string, []error) {
	labels := make(map[string]string, len(m))
	var errs []error
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

// LabelsFromTags converts a slice of strings of the form `k=v` to a map where the
// key is `k` and the value is `v`. If any element of the slice does not
// have that form or if `k` is not a valid kubernetes label name or if `v`
// is not a valid kubernetes label value, it will not be inserted into the
// map and instead generate an error which will be appended to the second
// return value.
//
// See https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
func LabelsFromTags(tags []string) (map[string]string, []error) {
	m, errs1 := TagMapFromTags(tags)
	labels, errs2 := labelsFromTagMap(m)
	return labels, append(errs1, errs2...)
}

// MatchJobTags reports whether each tag key in `agentTags` is also
// present in `jobTags`, and the tag value in `jobTags` is either "*" or the
// same as the tag value in `agentTags`.
//
// In the future, this may be expanded to: if the tag value `agentTags` is in some
// set of strings defined by the tag value in `jobTags` (eg a glob or regex)
// See https://buildkite.com/docs/agent/v3/cli-start#agent-targeting

func MatchJobTags(agentTags, jobTags map[string]string) bool {
	if len(agentTags) != len(jobTags) {
		return false
	}
	for k, v := range agentTags {
		jobTagValue, exists := jobTags[k]
		if !exists {
			return false
		}
		if jobTagValue != "*" && jobTagValue != v {
			return false
		}
	}
	return true
}

// ScanLabels returns an iterator over all labels that are tags.
func ScanLabels(labels map[string]string) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for key, value := range labels {
			k, has := strings.CutPrefix(key, "tag.buildkite.com/")
			if !has {
				continue
			}
			if !yield(k, value) {
				return
			}
		}
	}
}

// TagsFromLabels converts job or pod labels into a slice of agent/job tags.
func TagsFromLabels(labels map[string]string) (tags []string) {
	for key, value := range ScanLabels(labels) {
		tags = append(tags, key+"="+value)
	}
	return tags
}
