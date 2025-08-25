package stacksapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type RegisterStackParams struct {
	Key      string            `json:"key,omitempty"`       // The key to call the stack. Required.
	Type     StackType         `json:"type,omitempty"`      // A type for the stack. Required.
	QueueKey string            `json:"queue_key,omitempty"` // The key of a queue for the stack to listen to. Required
	Metadata map[string]string `json:"metadata"`            // Key-value metadata to associate with the queue. Required, but can be empty
}

type Stack struct {
	ID              string            `json:"id"`
	OrganizationID  string            `json:"organization_id"`
	ClusterQueueKey string            `json:"cluster_queue_key"`
	Key             string            `json:"key"`
	Type            StackType         `json:"type"`
	Metadata        map[string]string `json:"metadata"`
	LastConnectedOn *time.Time        `json:"last_connected_on"`
	State           StackState        `json:"state"`

	apiClient *Client
}

// RegisterStack registers a new stack with the Buildkite Stacks API. The API methods defined on the Stack struct inherit
// the [Client] struct used to register this stack.
//
// A stack with the same key can safely be re-registered as many times as necessary.
func (c *Client) RegisterStack(ctx context.Context, body RegisterStackParams, opts ...RequestOption) (*Stack, error) {
	req, err := c.NewRequest(ctx, http.MethodPost, "/stacks/register", body, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	var stack Stack
	resp, err := c.Do(ctx, req, &stack)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	stack.apiClient = c

	return &stack, nil
}

// Deregister lets the Buildkite Stacks API know that a stack is exiting gracefully. Stacks should always call Deregister
// before exiting cleanly. If a stack exits before calling deregister, it will be marked as lost in Buildkite.
func (s *Stack) Deregister(ctx context.Context, opts ...RequestOption) error {
	req, err := s.apiClient.NewRequest(ctx, http.MethodPost, fmt.Sprintf("/stacks/%s/deregister", s.Key), nil, opts...)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.apiClient.Do(ctx, req, nil)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
