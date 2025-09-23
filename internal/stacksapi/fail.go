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
	path := fmt.Sprintf("/stacks/%s/jobs/%s/fail", failJobReq.StackKey, failJobReq.JobUUID)
	req, err := c.newRequest(ctx, http.MethodPost, path, failJobReq, opts...)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	_, _, err = do[struct{}](ctx, c, req)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}

	return nil
}
