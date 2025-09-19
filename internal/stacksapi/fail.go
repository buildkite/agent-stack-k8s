package stacksapi

import (
	"context"
	"fmt"
	"net/http"
)

type FailJobRequest struct {
	StackKey    string `json:"-"` // The key to call the stack. Required.
	JobUUID     string `json:"-"` // The uuid of a job
	ExitStatus  int    `json:"exit_status,omitzero"`
	ErrorDetail string `json:"error_detail"` // An error detail string less than 4KB.
}

func (c *Client) FailJob(ctx context.Context, failJobReq FailJobRequest, opts ...RequestOption) error {
	const croppedMessage = "… (Error message was cropped because it exceeded the max size)"
	const maxErrorDetailSize = (4 * 1024) - len(croppedMessage) // 4KB

	if len(failJobReq.ErrorDetail) > maxErrorDetailSize {
		c.logger.Warn("ErrorDetail exceeds 4KB limit, cropping", "original_size", len(failJobReq.ErrorDetail), "cropped_size", maxErrorDetailSize)
		failJobReq.ErrorDetail = failJobReq.ErrorDetail[:maxErrorDetailSize] + croppedMessage
	}

	path := fmt.Sprintf("/stacks/%s/jobs/%s/fail", failJobReq.StackKey, failJobReq.JobUUID)
	req, err := c.newRequest(ctx, http.MethodPost, path, failJobReq, opts...)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.do(ctx, req, nil)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
