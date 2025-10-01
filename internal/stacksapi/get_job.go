package stacksapi

import (
	"context"
	"fmt"
	"net/http"
)

type GetJobRequest struct {
	StackKey string `json:"-"` // The key to call the stack. Required.
	JobUUID  string `json:"-"` // The uuid of a job. Required.
}

type GetJobResponse struct {
	ID      string            `json:"id"`
	Env     map[string]string `json:"env"`
	Command string            `json:"command"`
}

// GetJob return Job's runtime data (env and command) for a single job.
func (c *Client) GetJob(ctx context.Context, getJobReq GetJobRequest, opts ...RequestOption) (*GetJobResponse, http.Header, error) {
	path := constructPath("/stacks/%s/jobs/%s", getJobReq.StackKey, getJobReq.JobUUID)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, header, err := do[GetJobResponse](ctx, c, req)
	if err != nil {
		return nil, header, fmt.Errorf("get job: %w", err)
	}

	return resp, header, nil
}
