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

	// This impacts a number of endpoints' query parameters
	reservation bool
	UseStackAPI bool
}

type AgentClientOpts struct {
	Token           string
	Endpoint        string
	ClusterID       string
	Queue           string
	StackID         string
	AgentQueryRules []string
	UseReservation  bool
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
		reservation: opts.UseReservation,
		UseStackAPI: opts.UseStacksAPI,
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

		stack, err := client.stacksAPIClient.RegisterStack(ctx, stacksapi.RegisterStackRequest{
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
func (c *AgentClient) GetScheduledJobs(ctx context.Context, afterCursor string, limit int) (result *AgentScheduledJobs, retryAfter time.Duration, err error) {
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

	result, retryAfter, err = decodeResponse[AgentScheduledJobs](resp)
	if result != nil {
		now := time.Now()
		for _, j := range result.Jobs {
			j.QueriedAt = now
			if c.queue == defaultQueueKey {
				// When we poll from default queue, we don't know the queue key, so in rest of the system queue="".
				// The job might contain a queue key `agents: queue: default`, in that case it will cause mismatch in local
				// job queue key "" vs our configuration queue key "default".
				// So this is forcing them to be equal.
				j.AgentQueryRules = agenttags.RemoveTag(j.AgentQueryRules, "queue")
			} else {
				// Ensure the job has the queue tag. We queried a queue-specific
				// endpoint, but it may be the default queue, which doesn't require
				// `agents: queue: ...`, so the queue tag might not be present.
				j.AgentQueryRules = agenttags.SetTag(j.AgentQueryRules, "queue", c.queue)
			}
		}
	}
	return result, retryAfter, err
}

// AgentJob is enough info to run a job (includes command and env).
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

	if c.reservation {
		v := make(url.Values)
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

// ReserveJobBatchResult describes the result of a batch job reservation.
type BatchReserveJobsResult struct {
	ReservedJobUUIDs    []string `json:"reserved"`
	NotReservedJobUUIDs []string `json:"not_reserved"`
}

// ReserveJobs reserves a batch of jobs.
func (c *AgentClient) ReserveJobs(ctx context.Context, ids []string) (result *BatchReserveJobsResult, retryAfter time.Duration, err error) {
	if !c.reservation {
		// We don't expect this to happen in prod. It's mainly a defensive mechanism.
		return nil, 0, errors.New("reservation not enabled")
	}
	u := c.endpoint.JoinPath(
		"clusters", railsPathEscape(c.clusterID),
		"queues", railsPathEscape(c.queue),
		"jobs", "mass_reserve",
	)

	requestBody := map[string][]string{
		"job_uuids": ids,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, 0, err
	}

	// TODO: there is a size limit in this reserve api. we need to batch these by 1000
	// TODO: as more support coming to support reservation updates, we will override the default timeout to a much smaller
	// value.
	req, err := http.NewRequestWithContext(ctx, "PUT", u.String(), strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	return decodeResponse[BatchReserveJobsResult](resp)
}

func (c *AgentClient) DeregisterStack(ctx context.Context) error {
	if c.stacksAPIClient == nil {
		return nil
	}

	return c.stacksAPIClient.DeregisterStack(ctx, c.stack.Key, stacksapi.WithNoRetry())
}

func (c *AgentClient) FailJob(ctx context.Context, jobUUID string, errorDetail string) error {
	if !c.UseStackAPI {
		return fmt.Errorf("cannot call FailJob when useStackAPI is off")
	}

	req := stacksapi.FailJobRequest{
		StackKey:    c.stack.Key,
		JobUUID:     jobUUID,
		ErrorDetail: errorDetail,
	}

	return c.stacksAPIClient.FailJob(ctx, req)
}

func decodeResponse[T any](resp *http.Response) (result *T, retryAfter time.Duration, err error) {
	retryAfter = readRetryAfter(resp)

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

func readRetryAfter(resp *http.Response) time.Duration {
	h := resp.Header.Get("Retry-After")
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
