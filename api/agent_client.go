package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func NewAgentClient(token, endpoint, orgSlug, clusterID, queue string, agentQueryRules []string) (*AgentClient, error) {
	if endpoint == "" {
		endpoint = "https://agent.buildkite.com/v3"
	}
	if clusterID == "" {
		clusterID = "unclustered"
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	return &AgentClient{
		endpoint: endpointURL,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: NewLogger(NewAuthedTransportWithToken(http.DefaultTransport, token)),
		},
		orgSlug:   orgSlug,
		clusterID: clusterID,
		queue:     queue,
	}, nil
}

// AgentClient is a client for Agent API methods for retrieving jobs.
type AgentClient struct {
	endpoint   *url.URL
	httpClient *http.Client
	orgSlug    string
	clusterID  string // or "unclustered"
	queue      string
}

// AgentError is the JSON object of the response body returned when the HTTP
// status code is not success. It doubles as a Go error.
type AgentError struct {
	Message string `json:"message"`

	Status     string `json:"-"`
	StatusCode int    `json:"-"`
}

func (e AgentError) Error() string { return e.Message }

// Permanent reports if the error is not transient (i.e. a 4xx status code that
// isn't 429).
func (e AgentError) Permanent() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500 && e.StatusCode != 429
}

// IsPermanentError reports whether err is a non-nil Permanent AgentError.
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}
	var agentErr AgentError
	if !errors.As(err, &agentErr) {
		return false
	}
	return agentErr.Permanent()
}

// AgentScheduledJobs is the response object from the scheduled_jobs path.
type AgentScheduledJobs struct {
	Jobs         []*AgentScheduledJob `json:"jobs"`
	ClusterQueue struct {
		ID             string `json:"id"`
		DispatchPaused bool   `json:"dispatch_paused"`
	} `json:"cluster_queue"`
	PageInfo struct {
		EndCursor   string `json:"end_cursor"`
		HasNextPage bool   `json:"has_next_page"`
	} `json:"page_info"`
}

// AgentScheduledJob is enough job information to prioritise and decide on
// sending a job through the controller's pipeline to run. It doesn't have
// enough info to actually run the job.
type AgentScheduledJob struct {
	ID              string   `json:"id"`
	Priority        int      `json:"priority"`
	AgentQueryRules []string `json:"agent_query_rules"`

	// added by GetScheduledJobs to track end-to-end latency
	QueriedAt time.Time `json:"-"`
}

// GetScheduledJobs gets a page of jobs that could be run.
func (c *AgentClient) GetScheduledJobs(ctx context.Context, afterCursor string, limit int) (*AgentScheduledJobs, error) {
	u := c.endpoint.JoinPath(
		"clusters", railsPathEscape(c.clusterID),
		"queues", railsPathEscape(c.queue),
		"scheduled_jobs",
	)
	v := make(url.Values)
	if afterCursor != "" {
		v.Add("after", afterCursor)
	}
	v.Add("limit", strconv.Itoa(limit))
	u.RawQuery = v.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}

	jobs := new(AgentScheduledJobs)
	if err := json.NewDecoder(resp.Body).Decode(jobs); err != nil {
		return nil, err
	}

	now := time.Now()
	for _, j := range jobs.Jobs {
		j.QueriedAt = now
	}
	return jobs, nil
}

// AgentJob is enough info to run a job (includes command and env).
type AgentJob struct {
	ID      string            `json:"id"`
	Command string            `json:"command"`
	Env     map[string]string `json:"env"`
}

// GetJobToRun gets info about a specific job needed to run it.
func (c *AgentClient) GetJobToRun(ctx context.Context, id string) (*AgentJob, error) {
	u := c.endpoint.JoinPath(
		"clusters", railsPathEscape(c.clusterID),
		"queues", railsPathEscape(c.queue),
		"scheduled_jobs", railsPathEscape(id),
	)

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// It's no longer available to run (cancelled, probably).
		return nil, nil
	}

	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}

	job := new(AgentJob)
	if err := json.NewDecoder(resp.Body).Decode(job); err != nil {
		return nil, err
	}
	return job, nil
}

// AgentJobState describes the current state of a job.
type AgentJobState struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// GetJobState gets the state of a specific job.
func (c *AgentClient) GetJobState(ctx context.Context, id string) (*AgentJobState, error) {
	u := c.endpoint.JoinPath(
		"clusters", railsPathEscape(c.clusterID),
		"queues", railsPathEscape(c.queue),
		"jobs", railsPathEscape(id),
		"state",
	)

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}

	job := new(AgentJobState)
	if err := json.NewDecoder(resp.Body).Decode(job); err != nil {
		return nil, err
	}
	return job, nil
}

func decodeError(resp *http.Response) error {
	ae := AgentError{
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
	}
	if err := json.NewDecoder(resp.Body).Decode(&ae); err != nil {
		return fmt.Errorf("while decoding error response on %q: %w", resp.Status, err)
	}
	return ae
}

// Rails doesn't accept dots in some path segments.
func railsPathEscape(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), ".", "%2E")
}
