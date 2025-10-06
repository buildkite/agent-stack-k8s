package stacksapi

import (
	"context"
	"fmt"
	"net/http"
)

type FinishJobRequest struct {
	StackKey   string `json:"-"`
	JobUUID    string `json:"-"`
	ExitStatus int    `json:"exit_status,omitempty"`
	Detail     string `json:"detail"`
}

func (c *Client) FinishJob(ctx context.Context, finishJobReq FinishJobRequest, opts ...RequestOption) (http.Header, error) {
	const croppedMessage = "… (Message was cropped because it exceeded the max size)"
	const maxDetailSize = (4 * 1024) - len(croppedMessage)

	if len(finishJobReq.Detail) > maxDetailSize {
		c.logger.Warn("Detail exceeds 4KB limit, cropping", "original_size", len(finishJobReq.Detail), "cropped_size", maxDetailSize)
		finishJobReq.Detail = finishJobReq.Detail[:maxDetailSize] + croppedMessage
	}

	path := constructPath("/stacks/%s/jobs/%s/finish", finishJobReq.StackKey, finishJobReq.JobUUID)
	req, err := c.newRequest(ctx, http.MethodPost, path, finishJobReq, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	_, header, err := do[struct{}](ctx, c, req)
	if err != nil {
		return nil, fmt.Errorf("finish job: %w", err)
	}

	return header, nil
}
