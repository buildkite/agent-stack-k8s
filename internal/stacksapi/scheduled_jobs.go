package stacksapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Pipeline represents the pipeline that created a job.
type Pipeline struct {
	Slug string `json:"slug"`
	UUID string `json:"uuid"`
}

// Build represents the build that created a job.
type Build struct {
	UUID   string `json:"uuid"`
	Number int    `json:"number"`
	Branch string `json:"branch"`
}

// Step represents the step that created a job.
type Step struct {
	Key string `json:"key"`
}

// ScheduledJob represents the metadata for a job that's in the scheduled state.
type ScheduledJob struct {
	ID              string    `json:"id"`                // The UUID of the job
	Priority        int       `json:"priority"`          // The priority of the job; higher priority jobs should be scheduled first
	AgentQueryRules []string  `json:"agent_query_rules"` // The agent tags that must be matched to run the job
	ScheduledAt     time.Time `json:"scheduled_at"`      // When the job was scheduled
	Pipeline        Pipeline  `json:"pipeline"`          // The pipeline the job belongs to
	Build           Build     `json:"build"`             // The build the job belongs to
	Step            Step      `json:"step"`              // The step the job was created by
}

// ClusterQueue represents a cluster queue in the Buildkite Stacks system.
type ClusterQueue struct {
	ID     string `json:"id"`
	Paused bool   `json:"paused"`
}

// PageInfo contains information about pagination in a list response.
type PageInfo struct {
	HasNextPage bool   `json:"has_next_page"` // Whether there are more pages of results
	EndCursor   string `json:"end_cursor"`    // The cursor to use to fetch the next page of results
}

// ListScheduledJobsResponse is the output type for [ListScheduledJobs]. It contains a list of jobs and pagination information.
type ListScheduledJobsResponse struct {
	Jobs         []ScheduledJob `json:"jobs"`          // A list of jobs that are in the scheduled state
	ClusterQueue ClusterQueue   `json:"cluster_queue"` // The cluster queue the jobs were fetched from
	PageInfo     PageInfo       `json:"page_info"`     // Information about pagination
}

// ListScheduledJobsRequest is the input type for [ListScheduledJobs]. The StackKey and ClusterQueueKey fields are required.
type ListScheduledJobsRequest struct {
	StackKey        string // The key of the stack calling this endpoint. Required
	ClusterQueueKey string // The key of the cluster queue to list jobs from. Required
	PageSize        int    // The number of jobs to return. Optional, defaults to a sensible value on the backend
	StartCursor     string // The cursor to start the page from. Optional, defaults to the start of the list
}

// ListScheduledJobs lists jobs in the scheduled state from a specific cluster queue. The StackKey and ClusterQueueKey
// fields of the request are required. This method will only return a single page of results. To retrieve additional
// pages, call this method again with the EndCursor field set to the EndCursor value from the previous response's PageInfo.
func (c *Client) ListScheduledJobs(ctx context.Context, listReq ListScheduledJobsRequest, opts ...RequestOption) (*ListScheduledJobsResponse, http.Header, error) {
	path := constructPath("/stacks/%s/scheduled-jobs", listReq.StackKey)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("queue_key", listReq.ClusterQueueKey)

	if listReq.PageSize > 0 {
		q.Add("limit", fmt.Sprintf("%d", listReq.PageSize))
	}

	if listReq.StartCursor != "" {
		q.Add("after", listReq.StartCursor)
	}

	req.URL.RawQuery = q.Encode()

	listJobsResp, header, err := do[ListScheduledJobsResponse](ctx, c, req)
	if err != nil {
		return nil, nil, fmt.Errorf("list scheduled jobs: %w", err)
	}

	return listJobsResp, header, nil
}

// BatchReserveJobsRequest is the input type for [BatchReserveJobs]. The StackKey and JobUUIDs fields are required.
type BatchReserveJobsRequest struct {
	StackKey                 string   `json:"-"`                                   // The key of the stack calling this endpoint. Required.
	JobUUIDs                 []string `json:"job_uuids"`                           // The UUIDs of the jobs to reserve. Required.
	ReservationExpirySeconds int      `json:"reservation_expiry_seconds,omitzero"` // The number of seconds until the reservation expires. Optional, defaults to 300 (5 minutes) if not set.
}

// BatchReserveJobsResponse is the output type for [BatchReserveJobs]. It contains lists of successfully reserved and not reserved job UUIDs.
type BatchReserveJobsResponse struct {
	Reserved    []string `json:"reserved"`     // The UUIDs of the jobs that were successfully reserved
	NotReserved []string `json:"not_reserved"` // The UUIDs of the jobs that could not be reserved (e.g. because they were already reserved by another stack, or because they're no longer in a reservable state)
}

// BatchReserveJobs attempts to reserve a list of jobs for processing by the calling stack. The StackKey and JobUUIDs
// fields of the request are required. If a job cannot be reserved (for example, because it has already been reserved
// by another stack, or because it is no longer in a reservable state), it will be included in the NotReserved list
// in the response. Note that even if none of the jobs could be reserved, this method will still return a nil error,
// as long as the request itself was valid.
func (c *Client) BatchReserveJobs(ctx context.Context, reserveReq BatchReserveJobsRequest, opts ...RequestOption) (*BatchReserveJobsResponse, http.Header, error) {
	path := constructPath("/stacks/%s/scheduled-jobs/batch-reserve", reserveReq.StackKey)

	req, err := c.newRequest(ctx, http.MethodPut, path, reserveReq, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	reserveJobsResp, header, err := do[BatchReserveJobsResponse](ctx, c, req)
	if err != nil {
		return nil, nil, fmt.Errorf("batch reserve jobs: %w", err)
	}

	return reserveJobsResp, header, nil
}
