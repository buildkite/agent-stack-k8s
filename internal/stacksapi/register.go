package stacksapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// RegisterStackRequest is the input type for [RegisterStack]. All of its fields are required.
type RegisterStackRequest struct {
	Key      string            `json:"key,omitempty"`       // The key to call the stack. Required.
	Type     StackType         `json:"type,omitempty"`      // A type for the stack. Required.
	QueueKey string            `json:"queue_key,omitempty"` // The key of a queue for the stack to listen to. Required
	Metadata map[string]string `json:"metadata"`            // Key-value metadata to associate with the queue. Required, but can be empty
}

// RegisterStackResponse is the output type for [RegisterStack]. It contains all the information about a registered stack
type RegisterStackResponse struct {
	ID               string            `json:"id"`
	OrganizationUUID string            `json:"organization_uuid"`
	ClusterQueueKey  string            `json:"cluster_queue_key"`
	Key              string            `json:"key"`
	Type             StackType         `json:"type"`
	Metadata         map[string]string `json:"metadata"`
	LastConnectedOn  *time.Time        `json:"last_connected_on"`
	State            StackState        `json:"state"`
}

// RegisterStack registers a new stack with the Buildkite Stacks API. A stack with the same key can safely be
// re-registered as many times as necessary.
func (c *Client) RegisterStack(ctx context.Context, body RegisterStackRequest, opts ...RequestOption) (*RegisterStackResponse, http.Header, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "stacks/register", body, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	stack, header, err := do[RegisterStackResponse](ctx, c, req)
	if err != nil {
		return nil, nil, fmt.Errorf("register stack: %w", err)
	}

	return stack, header, nil
}

// DeregisterStack informs the Buildkite Stacks API that a stack is exiting cleanly.
func (c *Client) DeregisterStack(ctx context.Context, stackKey string, opts ...RequestOption) (http.Header, error) {
	path := constructPath("/stacks/%s/deregister", stackKey)
	req, err := c.newRequest(ctx, http.MethodPost, path, nil, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	_, header, err := do[struct{}](ctx, c, req)
	if err != nil {
		return nil, fmt.Errorf("deregister stack: %w", err)
	}

	return header, nil
}
