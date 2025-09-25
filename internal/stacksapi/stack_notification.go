package stacksapi

import (
	"context"
	"fmt"
	"net/http"
)

type CreateStackNotificationRequest struct {
	StackKey string `json:"-"`      // The key to call the stack. Required.
	JobUUID  string `json:"-"`      // The uuid of a job
	Detail   string `json:"detail"` // Short notification message (max length 255)
}

func (c *Client) CreateStackNotification(ctx context.Context, req CreateStackNotificationRequest, opts ...RequestOption) (http.Header, error) {
	const croppedMessage = "… (cropped)"
	const maxDetailSize = 255 - len(croppedMessage)

	if len(req.Detail) > maxDetailSize {
		c.logger.Warn("Stack notification detail exceeds character limit, cropping", "original_size", len(req.Detail))
		req.Detail = req.Detail[:maxDetailSize] + croppedMessage
	}

	path := constructPath("/stacks/%s/jobs/%s/stack_notifications", req.StackKey, req.JobUUID)
	httpReq, err := c.newRequest(ctx, http.MethodPost, path, req, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	_, header, err := do[struct{}](ctx, c, httpReq)
	if err != nil {
		return nil, fmt.Errorf("create stack notification: %w", err)
	}

	return header, nil
}
