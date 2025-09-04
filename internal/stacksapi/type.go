package stacksapi

// StackType enumerates a few broad categories of stack. Use of [StackTypeKubernetes] and [StackTypeElastic] are discouraged
// for non-first-party stacks.
type StackType string

const (
	StackTypeKubernetes StackType = "kubernetes" // Reserved for use by the first-party agent-stack-k8s
	StackTypeElastic    StackType = "elastic"    // Reserved for use by the first-party elastic-stack-for-aws

	// For use by custom stacks. If you're reading these docs and you don't work at buildkite, this is almost certainly the
	// stack type you should be using, even if your stack is backed by Kubernetes
	StackTypeCustom StackType = "custom"
)

// StackState enumerates the connection status of a stack
type StackState string

const (
	StackStateConnected    StackState = "connected"
	StackStateDisconnected StackState = "disconnected"
	StackStateLost         StackState = "lost"
)
