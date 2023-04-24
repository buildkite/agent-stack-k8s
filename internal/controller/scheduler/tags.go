package scheduler

import (
	"strings"

	"go.uber.org/zap"
)

// a valid label must be an empty string or consist of alphanumeric characters,
// '-', '_' or '.', and must start and end with an alphanumeric character (e.g.
// 'MyValue',  or 'my_value',  or '12345', regex used for validation is
// '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')
func TagToLabel(tag string) string {
	return strings.Replace(tag, "=", "/", 1)
}

func TagsToLabels(logger *zap.Logger, tags []string) map[string]string {
	labels := make(map[string]string, len(tags))
	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) != 2 {
			logger.Warn("invalid agent tag", zap.String("tag", tag))
			continue
		}

		if len(parts[0]) == 0 || len(parts[0]) > 63 {
			logger.Warn("agent tag key not between 1 and 63 chars", zap.String("tag-key", parts[0]))
			continue
		}

		if len(parts[1]) == 0 || len(parts[1]) > 63 {
			logger.Warn("agent tag value not between 1 and 63 chars", zap.String("tag-value", parts[1]))
			continue
		}

		labels["buildkite.com/"+parts[0]] = parts[1]
	}

	return labels
}
