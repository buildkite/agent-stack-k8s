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
func (c *Client) RegisterStack(ctx context.Context, body RegisterStackRequest, opts ...RequestOption) (RegisterStackResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "stacks/register", body, opts...)
	if err != nil {
		return RegisterStackResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	var stack RegisterStackResponse
	resp, err := c.do(ctx, req, &stack)
	if err != nil {
		return RegisterStackResponse{}, fmt.Errorf("register stack: %w", err)
	}
	defer resp.Body.Close()

	return stack, nil
}

// DeregisterStack informs the Buildkite Stacks API that a stack is exiting cleanly.
func (c *Client) DeregisterStack(ctx context.Context, stackKey string, opts ...RequestOption) error {
	path := fmt.Sprintf("/stacks/%s/deregister", stackKey)
	req, err := c.newRequest(ctx, http.MethodPost, path, nil, opts...)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.do(ctx, req, nil)
	if err != nil {
		return fmt.Errorf("deregister stack: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
