package stacksapi

type StackType string

const (
	StackTypeKubernetes StackType = "kubernetes"
	StackTypeElastic    StackType = "elastic"
	StackTypeCustom     StackType = "custom"
)

type StackState string

const (
	StackStateConnected    StackState = "connected"
	StackStateDisconnected StackState = "disconnected"
	StackStateLost         StackState = "lost"
)
