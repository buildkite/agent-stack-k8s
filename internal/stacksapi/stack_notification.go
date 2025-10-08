package stacksapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type StackNotification struct {
	JobUUID   string    `json:"job_uuid"`
	Detail    string    `json:"detail"`
	Timestamp time.Time `json:"timestamp,omitzero"`
}

type CreateStackNotificationsRequest struct {
	StackKey      string              `json:"-"`
	Notifications []StackNotification `json:"notifications"`
}

type StackNotificationError struct {
	Error   string `json:"error"`
	Indexes []int  `json:"indexes"`
}

type CreateStackNotificationsResponse struct {
	Errors []StackNotificationError `json:"errors"`
}

func (c *Client) CreateStackNotifications(ctx context.Context, req CreateStackNotificationsRequest, opts ...RequestOption) (*CreateStackNotificationsResponse, http.Header, error) {
	path := constructPath("/stacks/%s/notifications", req.StackKey)
	httpReq, err := c.newRequest(ctx, http.MethodPost, path, req, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, header, err := do[CreateStackNotificationsResponse](ctx, c, httpReq)
	if err != nil {
		return nil, header, fmt.Errorf("create stack notifications: %w", err)
	}

	return resp, header, nil
}
