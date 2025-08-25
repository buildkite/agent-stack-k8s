package stacksapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-querystring/query"
)

type ListScheduledJobsParams struct {
	QueueKey    string `url:"queue_key,omitempty"`    // The key of the queue to list
	AfterCursor string `url:"after_cursor,omitempty"` // Cursor for pagination, to get the next page of results
}

type ScheduledJob struct {
	ID              string    `json:"id"`
	Priority        int       `json:"priority"`          // Priority of the job, higher values are more important
	AgentQueryRules []string  `json:"agent_query_rules"` // Rules to match agents that can run this job
	ScheduledAt     time.Time `json:"scheduled_at"`      // When the job was scheduled
}

type ListScheduledJobsResponse struct {
	Jobs         []ScheduledJob `json:"jobs"`
	ClusterQueue string         `json:"cluster_queue"`
	PageInfo     struct {
		HasNextPage bool   `json:"has_next_page"` // Whether there are more pages of results
		EndCursor   string `json:"end_cursor"`    // Cursor for the last item in this page
	} `json:"page_info"`
}

// ListScheduledJobs retrieves a list of scheduled jobs for the stack. This method returns a single page of results, and
// the second return value can be used to continue paginating. For example:
//
//	 var allJobs []ScheduledJob
//		for {
//			jobs, nextParams, err := s.ListScheduledJobs(ctx, params)
//			if err != nil {
//				return nil, fmt.Errorf("failed to list scheduled jobs: %w", err)
//			}
//			allJobs = append(allJobs, jobs...)
//			if nextParams == nil {
//				break // No more pages
//			}
//			params = *nextParams // Update params for the next iteration
//		}
func (s *Stack) ListScheduledJobs(ctx context.Context, params ListScheduledJobsParams, opts ...RequestOption) ([]ScheduledJob, *ListScheduledJobsParams, error) {
	path := fmt.Sprintf("/stacks/%s/scheduled_jobs", s.Key)
	req, err := s.apiClient.NewRequest(ctx, http.MethodGet, path, nil, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	qparams, err := query.Values(params)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode query parameters: %w", err)
	}

	req.URL.RawQuery = qparams.Encode()

	var listResp ListScheduledJobsResponse
	resp, err := s.apiClient.Do(ctx, req, &listResp)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if !listResp.PageInfo.HasNextPage {
		// No more pages, return the jobs and nil for nextParams
		return listResp.Jobs, nil, nil
	}

	nextParams := &ListScheduledJobsParams{
		QueueKey:    params.QueueKey,
		AfterCursor: listResp.PageInfo.EndCursor,
	}

	return listResp.Jobs, nextParams, nil
}

// PaginateAllScheduledJobs retrieves all scheduled jobs for the stack, across multiple pages if necessary. It will return
// the full list of all scheduled jobs available at the time of the first HTTP call, and will return an error if any
// requests fail.
func (s *Stack) PaginateAllScheduledJobs(ctx context.Context, params ListScheduledJobsParams, opts ...RequestOption) ([]ScheduledJob, error) {
	var allJobs []ScheduledJob

	for {
		jobs, nextParams, err := s.ListScheduledJobs(ctx, params, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to list scheduled jobs: %w", err)
		}

		allJobs = append(allJobs, jobs...)

		if nextParams == nil {
			break // No more pages
		}

		params = *nextParams // Update params for the next iteration
	}

	return allJobs, nil
}
