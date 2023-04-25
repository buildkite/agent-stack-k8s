package agenttags

import (
	"fmt"
	"strings"
)

// a valid label must be an empty string or consist of alphanumeric characters,
// '-', '_' or '.', and must start and end with an alphanumeric character (e.g.
// 'MyValue',  or 'my_value',  or '12345', regex used for validation is
// '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')
func TagToLabel(tag string) string {
	return strings.Replace(tag, "=", "_", 1)
}

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
		if len(k) == 0 || len(k) > 63 {
			errs = append(errs, fmt.Errorf("invalid agent tag key: %q", k))
			continue
		}

		if len(v) == 0 || len(v) > 63 {
			errs = append(errs, fmt.Errorf("invalid agent tag val: %q", v))
			continue
		}

		labels["buildkite.com/"+k] = v
	}
	return labels, errs
}

func ToLabels(tags []string) (map[string]string, []error) {
	m, errs1 := ToMap(tags)
	labels, errs2 := mapToLabels(m)
	errs := append(errs1, errs2...)
	return labels, errs
}
