package stacksapi

import (
	"context"
	"fmt"
	"net/http"
)

type GetJobStatesRequest struct {
	StackKey string   `json:"-"`         // The key to call the stack. Required.
	JobUUIDs []string `json:"job_uuids"` // A list of job uuids
}

type GetJobStatesResponse struct {
	States map[string]string `json:"states"`
}

// GetJobStates query job states for a list of jobs
func (c *Client) GetJobStates(ctx context.Context, getJobStatesReq GetJobStatesRequest, opts ...RequestOption) (*GetJobStatesResponse, http.Header, error) {
	path := constructPath("/stacks/%s/jobs/get-states", getJobStatesReq.StackKey)
	req, err := c.newRequest(ctx, http.MethodPost, path, getJobStatesReq, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	jobStates, header, err := do[GetJobStatesResponse](ctx, c, req)
	if err != nil {
		return nil, nil, fmt.Errorf("get job states: %w", err)
	}

	return jobStates, header, nil
}
