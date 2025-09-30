package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/stacksapi"
	slogzap "github.com/samber/slog-zap/v2"
	"go.uber.org/zap"
)

// This is a special keyword supported on backend for polling from the current default queue in the cluster.
const defaultQueueKey = "_default"

// AgentClient is a client for Agent API methods for retrieving jobs.
type AgentClient struct {
	endpoint        *url.URL
	httpClient      *http.Client
	stacksAPIClient *stacksapi.Client

	clusterID string
	queue     string
	stack     *stacksapi.RegisterStackResponse

	logger      *zap.Logger
	useStackAPI bool
}

type AgentClientOpts struct {
	Token           string
	Endpoint        string
	ClusterID       string
	Queue           string
	StackID         string
	AgentQueryRules []string
	Logger          *zap.Logger
	UseStacksAPI    bool
}

func NewAgentClient(ctx context.Context, opts AgentClientOpts) (*AgentClient, error) {
	if opts.Endpoint == "" {
		opts.Endpoint = "https://agent.buildkite.com/v3"
	}

	if opts.ClusterID == "" {
		opts.ClusterID = "unclustered"
	}

	endpointURL, err := url.Parse(opts.Endpoint)
	if err != nil {
		return nil, err
	}

	if opts.Queue == "" {
		opts.Queue = defaultQueueKey
	}

	client := &AgentClient{
		endpoint: endpointURL,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: NewLogger(NewAuthedTransportWithToken(http.DefaultTransport, opts.Token)),
		},
		clusterID:   opts.ClusterID,
		queue:       opts.Queue,
		logger:      opts.Logger,
		useStackAPI: opts.UseStacksAPI,
	}

	if opts.UseStacksAPI {
		zapSlogHandler := slogzap.Option{Logger: opts.Logger}.NewZapHandler()

		client.stacksAPIClient, err = stacksapi.NewClient(
			opts.Token,
			stacksapi.WithLogger(slog.New(zapSlogHandler)),
			stacksapi.WithBaseURL(endpointURL),
		)
		if err != nil {
			return nil, fmt.Errorf("couldn't create Buildkite Stacks API client: %w", err)
		}

		stackKey := opts.StackID
		if stackKey == "" {
			stackKey = "agent-stack-k8s"
		}

		stack, _, err := client.stacksAPIClient.RegisterStack(ctx, stacksapi.RegisterStackRequest{
			Type:     stacksapi.StackTypeKubernetes,
			QueueKey: opts.Queue,
			Key:      stackKey,
			Metadata: map[string]string{},
		})
		if err != nil {
			return nil, fmt.Errorf("registering stack with Buildkite Stacks API: %w", err)
		}

		client.stack = stack
	}

	return client, nil
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

type ClusterQueue struct {
	ID     string `json:"id"`
	Paused bool   `json:"paused"`
}

type PageInfo struct {
	HasNextPage bool   `json:"has_next_page"`
	EndCursor   string `json:"end_cursor"`
}

// AgentScheduledJobs is the response object from the scheduled_jobs path.
type AgentScheduledJobs struct {
	Jobs         []*AgentScheduledJob `json:"jobs"`
	ClusterQueue ClusterQueue         `json:"cluster_queue"`
	PageInfo     PageInfo             `json:"page_info"`
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
func (c *AgentClient) GetScheduledJobs(ctx context.Context, afterCursor string, limit int) (*AgentScheduledJobs, time.Duration, error) {
	if c.UseStackAPI() {
		return c.getStackScheduledJobs(ctx, afterCursor, limit)
	}

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
		return nil, 0, err
	}
	defer resp.Body.Close()

	result, retryAfter, err := decodeResponse[AgentScheduledJobs](resp)
	if result != nil {
		now := time.Now()
		for _, j := range result.Jobs {
			j.QueriedAt = now
			j.AgentQueryRules = c.normaliseAgentQueryRules(j.AgentQueryRules)
		}
	}

	return result, retryAfter, err
}

func (c *AgentClient) normaliseAgentQueryRules(rules []string) []string {
	if c.queue == defaultQueueKey {
		// When we poll from default queue, we don't know the queue key, so in rest of the system queue="".
		// The job might contain a queue key `agents: queue: default`, in that case it will cause mismatch in local
		// job queue key "" vs our configuration queue key "default".
		return agenttags.RemoveTag(rules, "queue")
	} else {
		// Ensure the job has the queue tag. We queried a queue-specific
		// endpoint, but it may be the default queue, which doesn't require
		// `agents: queue: ...`, so the queue tag might not be present.
		return agenttags.SetTag(rules, "queue", c.queue)
	}
}

func (c *AgentClient) getStackScheduledJobs(ctx context.Context, afterCursor string, limit int) (*AgentScheduledJobs, time.Duration, error) {
	req := stacksapi.ListScheduledJobsRequest{
		StackKey:        c.stack.Key,
		ClusterQueueKey: c.stack.ClusterQueueKey,
		PageSize:        limit,
		StartCursor:     afterCursor,
	}

	// No retry, the monitor loop handles retries
	resp, header, err := c.stacksAPIClient.ListScheduledJobs(ctx, req, stacksapi.WithNoRetry())
	if err != nil {
		return nil, 0, err
	}

	jobs := make([]*AgentScheduledJob, 0, len(resp.Jobs))
	now := time.Now()
	for _, j := range resp.Jobs {
		asj := &AgentScheduledJob{
			ID:              j.ID,
			Priority:        j.Priority,
			AgentQueryRules: c.normaliseAgentQueryRules(j.AgentQueryRules),
			QueriedAt:       now,
		}
		jobs = append(jobs, asj)
	}

	return &AgentScheduledJobs{
		Jobs: jobs,
		ClusterQueue: ClusterQueue{
			ID:     resp.ClusterQueue.ID,
			Paused: resp.ClusterQueue.Paused,
		},
		PageInfo: PageInfo{
			HasNextPage: resp.PageInfo.HasNextPage,
			EndCursor:   resp.PageInfo.EndCursor,
		},
	}, readRetryAfter(header), nil
}

type AgentJob struct {
	ID      string            `json:"id"`
	Command string            `json:"command"`
	Env     map[string]string `json:"env"`
}

// GetJobToRun gets info about a specific job needed to run it.
func (c *AgentClient) GetJobToRun(ctx context.Context, id string) (result *AgentJob, retryAfter time.Duration, err error) {
	u := c.endpoint.JoinPath(
		"clusters", railsPathEscape(c.clusterID),
		"queues", railsPathEscape(c.queue),
		"scheduled_jobs", railsPathEscape(id),
	)

	// Usage of the stack API implies that we're reserving jobs, so we want to
	// include_reserved=true to be able to fetch jobs that we've reserved.
	if c.UseStackAPI() {
		v := u.Query()
		v.Add("include_reserved", "true")
		u.RawQuery = v.Encode()
	}

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// It's no longer available to run (cancelled, probably).
		return nil, 0, nil
	}

	return decodeResponse[AgentJob](resp)
}

// AgentJobState describes the current state of a job.
type AgentJobState struct {
	ID    string   `json:"id"`
	State JobState `json:"state"`
}

// GetJobState gets the state of a specific job.
func (c *AgentClient) GetJobState(ctx context.Context, id string) (result *AgentJobState, retryAfter time.Duration, err error) {
	// Use the batch API as a shim when useStackAPI is on.
	if c.UseStackAPI() {
		state, retryAfter, err := c.GetJobStates(ctx, []string{id})
		if err != nil {
			return nil, retryAfter, err
		}
		return &AgentJobState{
			ID:    id,
			State: state[id],
		}, retryAfter, nil
	}

	u := c.endpoint.JoinPath(
		"clusters", railsPathEscape(c.clusterID),
		"queues", railsPathEscape(c.queue),
		"jobs", railsPathEscape(id),
		"state",
	)

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	return decodeResponse[AgentJobState](resp)
}

// GetJobStates gets state for a batch of jobs.
func (c *AgentClient) GetJobStates(ctx context.Context, ids []string) (result map[string]JobState, retryAfter time.Duration, err error) {
	if !c.UseStackAPI() {
		// We don't expect this to happen in prod. It's mainly a defensive mechanism.
		return nil, 0, errors.New("cannot use batch job state loading without Stack API")
	}
	req := stacksapi.GetJobStatesRequest{
		StackKey: c.stack.Key,
		JobUUIDs: ids,
	}
	statesResp, header, err := c.stacksAPIClient.GetJobStates(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	result = make(map[string]JobState, len(statesResp.States))
	for k, v := range statesResp.States {
		result[k] = JobState(v)
	}
	return result, readRetryAfter(header), err
}

// ReserveJobs reserves a batch of jobs.
func (c *AgentClient) ReserveJobs(ctx context.Context, ids []string) (*stacksapi.BatchReserveJobsResponse, time.Duration, error) {
	if !c.UseStackAPI() {
		// We don't expect this to happen in prod. It's mainly a defensive mechanism.
		return nil, 0, errors.New("reservation not enabled")
	}

	reservations, header, err := c.stacksAPIClient.BatchReserveJobs(ctx, stacksapi.BatchReserveJobsRequest{
		StackKey: c.stack.Key,
		JobUUIDs: ids,
	}, stacksapi.WithNoRetry())
	if err != nil {
		return nil, 0, err
	}

	return reservations, readRetryAfter(header), nil
}

func (c *AgentClient) DeregisterStack(ctx context.Context) error {
	if c.stacksAPIClient == nil {
		return nil
	}

	_, err := c.stacksAPIClient.DeregisterStack(ctx, c.stack.Key, stacksapi.WithNoRetry())
	return err
}

// UseStackAPI returns whether the client is configured to use the Stack API.
func (c *AgentClient) UseStackAPI() bool {
	return c.useStackAPI
}

func (c *AgentClient) FailJob(ctx context.Context, jobUUID string, errorDetail string) error {
	if !c.UseStackAPI() {
		return fmt.Errorf("cannot call FailJob when useStackAPI is off")
	}

	req := stacksapi.FailJobRequest{
		StackKey:    c.stack.Key,
		JobUUID:     jobUUID,
		ErrorDetail: errorDetail,
	}

	_, err := c.stacksAPIClient.FailJob(ctx, req)
	return err
}

// CreateStackNotification creates a stack notification for a job.
// This is designed to be call and forget, error will be logged because we don't anticipate error handling
func (c *AgentClient) CreateStackNotification(ctx context.Context, jobUUID string, detail string) {
	if !c.UseStackAPI() {
		// No-op for the case for legacy API
		return
	}

	req := stacksapi.CreateStackNotificationRequest{
		StackKey: c.stack.Key,
		JobUUID:  jobUUID,
		Detail:   detail,
	}

	// No retry as we want to avoid any possible source of latency bump for job creation flow.
	// In the future this will be moved into a batched stack notification worker, we can turn back auto retry then
	_, err := c.stacksAPIClient.CreateStackNotification(ctx, req, stacksapi.WithNoRetry())
	if err != nil {
		c.logger.Warn("Failed creating stack notification", zap.Error(err))
	}
}

func decodeResponse[T any](resp *http.Response) (result *T, retryAfter time.Duration, err error) {
	retryAfter = readRetryAfter(resp.Header)

	if resp.StatusCode >= 400 {
		return nil, retryAfter, decodeError(resp)
	}

	result = new(T)
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, retryAfter, err
	}
	return result, retryAfter, nil
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

func readRetryAfter(headers http.Header) time.Duration {
	h := headers.Get("Retry-After")
	if h == "" {
		return 0
	}
	ra, err := time.ParseDuration(h + "s")
	if err != nil {
		return 0
	}
	return ra
}

// Rails doesn't accept dots in some path segments.
func railsPathEscape(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), ".", "%2E")
}
