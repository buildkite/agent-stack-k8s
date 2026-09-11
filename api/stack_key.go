package api

import (
	"crypto/sha256"
	"fmt"

	"github.com/buildkite/stacksapi"
)

// scopedStackKey separates organization-wide API registration from the local
// Kubernetes controller ID. Both cluster and queue are necessary because queue
// keys can repeat across Buildkite clusters in the same organization.
func scopedStackKey(clusterID, queue, controllerID string) string {
	if controllerID == "" {
		controllerID = "agent-stack-k8s"
	}
	if queue == "" || isDefaultQueue(queue) {
		queue = stacksapi.DefaultQueue
	}
	// Quote each component so separators in input cannot create ambiguous scopes.
	// Hash the complete ID, including any part omitted from the readable prefix.
	digest := sha256.Sum256(fmt.Appendf(nil, "%q/%q/%q", clusterID, queue, controllerID))
	// Keep the key within the existing Helm controller-ID limit of 63 characters.
	return fmt.Sprintf("%s-%x", controllerID[:min(len(controllerID), 30)], digest[:16])
}
