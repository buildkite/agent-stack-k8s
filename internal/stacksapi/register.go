package stacksapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// RegisterStackParams is the input type for [RegisterStack]. All of its fields are required.
type RegisterStackParams struct {
	Key      string            `json:"key,omitempty"`       // The key to call the stack. Required.
	Type     StackType         `json:"type,omitempty"`      // A type for the stack. Required.
	QueueKey string            `json:"queue_key,omitempty"` // The key of a queue for the stack to listen to. Required
	Metadata map[string]string `json:"metadata"`            // Key-value metadata to associate with the queue. Required, but can be empty
}

// Stack is the output type for [RegisterStack]. It contains all the information about a registered stack
type Stack struct {
	ID              string            `json:"id"`
	OrganizationID  string            `json:"organization_id"`
	ClusterQueueKey string            `json:"cluster_queue_key"`
	Key             string            `json:"key"`
	Type            StackType         `json:"type"`
	Metadata        map[string]string `json:"metadata"`
	LastConnectedOn *time.Time        `json:"last_connected_on"`
	State           StackState        `json:"state"`
}

// RegisterStack registers a new stack with the Buildkite Stacks API. A stack with the same key can safely be
// re-registered as many times as necessary.
func (c *Client) RegisterStack(ctx context.Context, body RegisterStackParams, opts ...RequestOption) (Stack, error) {
	req, err := c.NewRequest(ctx, http.MethodPost, "/stacks/register", body, opts...)
	if err != nil {
		return Stack{}, fmt.Errorf("failed to create request: %w", err)
	}

	var stack Stack
	resp, err := c.Do(ctx, req, &stack)
	if err != nil {
		return Stack{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return stack, nil
}
