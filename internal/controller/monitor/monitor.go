package monitor

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"github.com/Khan/genqlient/graphql"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

type Monitor struct {
	gql    graphql.Client
	logger *zap.Logger
	cfg    Config
}

type Config struct {
	GraphQLEndpoint        string
	Namespace              string
	Token                  string
	ClusterUUID            string
	MaxInFlight            int
	JobCreationConcurrency int
	PollInterval           time.Duration
	StaleJobDataTimeout    time.Duration
	Org                    string
	Tags                   []string
	GraphQLResultsLimit    int
	EnableQueuePause       bool
	PaginationDepthLimit   int
}

func New(logger *zap.Logger, k8s kubernetes.Interface, cfg Config) (*Monitor, error) {
	graphqlClient := api.NewClient(cfg.Token, cfg.GraphQLEndpoint)

	// Poll no more frequently than every 1s (please don't DoS us).
	cfg.PollInterval = min(cfg.PollInterval, time.Second)

	// Default StaleJobDataTimeout to 10s.
	if cfg.StaleJobDataTimeout <= 0 {
		cfg.StaleJobDataTimeout = config.DefaultStaleJobDataTimeout
	}

	// Default CreationConcurrency to 5.
	if cfg.JobCreationConcurrency <= 0 {
		cfg.JobCreationConcurrency = config.DefaultJobCreationConcurrency
	}

	return &Monitor{
		gql:    graphqlClient,
		logger: logger,
		cfg:    cfg,
	}, nil
}

// jobResp is used to identify the response types from methods that call the GraphQL API
// in the cases where a cluster is specified or otherwise.
// The return types are are isomorphic, but this has been lost in the generation of the
// API calling methods. As such, the implementations should be syntacticaly identical, but
// semantically, they operate on different types.
type jobResp interface {
	OrganizationExists() bool
	CommandJobs() []*api.JobJobTypeCommand
}

type unclusteredJobResp api.GetScheduledJobsResponse

func (r unclusteredJobResp) OrganizationExists() bool {
	return r.Organization.Id != nil
}

func (r unclusteredJobResp) CommandJobs() []*api.JobJobTypeCommand {
	jobs := make([]*api.JobJobTypeCommand, 0, len(r.Organization.Jobs.Edges))
	for _, edge := range r.Organization.Jobs.Edges {
		jobs = append(jobs, edge.Node.(*api.JobJobTypeCommand))
	}
	return jobs
}

type clusteredJobResp api.GetScheduledJobsClusteredResponse

func (r clusteredJobResp) OrganizationExists() bool {
	return r.Organization.Id != nil
}

func (r clusteredJobResp) CommandJobs() []*api.JobJobTypeCommand {
	jobs := make([]*api.JobJobTypeCommand, 0, len(r.Organization.Jobs.Edges))
	for _, edge := range r.Organization.Jobs.Edges {
		jobs = append(jobs, edge.Node.(*api.JobJobTypeCommand))
	}
	return jobs
}

// getScheduledCommandJobs calls either the clustered or unclustered GraphQL API
// methods, depending on if a cluster uuid was provided in the config
func (m *Monitor) getScheduledCommandJobs(ctx context.Context, queue string) (jobResp jobResp, err error) {
	jobQueryCounter.Inc()
	start := time.Now()
	defer func() {
		jobQueryDurationHistogram.Observe(time.Since(start).Seconds())
		if err != nil {
			jobQueryErrorCounter.Inc()
		}
	}()

	var paginationDepth int
	var cursor string
	var order api.JobOrder

	if m.cfg.PaginationDepthLimit > 1 {
		// Change order to newest first in order to paginate
		order = api.JobOrderRecentlyCreated
	} else {
		// Default order is oldest first
		order = api.JobOrderRecentlyAssigned
	}

	if m.cfg.ClusterUUID == "" {
		var unclusteredJobs []api.GetScheduledJobsOrganizationJobsJobConnectionEdgesJobEdge
		var resp *api.GetScheduledJobsResponse

		for {
			resp, err = api.GetScheduledJobs(ctx, m.gql, m.cfg.Org, []string{fmt.Sprintf("queue=%s", queue)}, m.cfg.GraphQLResultsLimit, cursor, order)
			if err != nil {
				return unclusteredJobResp(*resp), err
			}

			endCursor := resp.Organization.Jobs.PageInfo.EndCursor
			unclusteredJobs = append(unclusteredJobs, resp.Organization.Jobs.Edges...)
			paginationDepth++

			if !resp.Organization.Jobs.PageInfo.HasNextPage || paginationDepth >= m.cfg.PaginationDepthLimit || endCursor == nil {
				break
			}

			cursor = *endCursor
		}

		// Create a combined response
		unclusteredResp := &api.GetScheduledJobsResponse{
			Organization: api.GetScheduledJobsOrganization{
				Id: resp.Organization.Id,
				Jobs: api.GetScheduledJobsOrganizationJobsJobConnection{
					Count:    len(unclusteredJobs),
					PageInfo: resp.Organization.Jobs.PageInfo,
					Edges:    unclusteredJobs,
				},
			},
		}

		return unclusteredJobResp(*unclusteredResp), err
	}

	var agentQueryRule []string
	if queue != "" {
		agentQueryRule = append(agentQueryRule, fmt.Sprintf("queue=%s", queue))
	}

	if m.cfg.EnableQueuePause {
		// TODO: use a more targeted query once one becomes available
		queues, err := api.GetClusterQueues(ctx, m.gql, m.cfg.Org, m.cfg.ClusterUUID, 100)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch cluster queues: %w", err)
		}

		isQueuePaused := false
		for _, edge := range queues.Organization.Cluster.Queues.Edges {
			if edge.Node.Key == queue {
				isQueuePaused = edge.Node.DispatchPaused
				break
			}
		}

		if isQueuePaused {
			return nil, fmt.Errorf("the queue %q is paused", queue)
		}
	}

	var clusteredJobs []api.GetScheduledJobsClusteredOrganizationJobsJobConnectionEdgesJobEdge
	var resp *api.GetScheduledJobsClusteredResponse

	for {
		resp, err = api.GetScheduledJobsClustered(
			ctx, m.gql, m.cfg.Org, agentQueryRule, encodeClusterGraphQLID(m.cfg.ClusterUUID), m.cfg.GraphQLResultsLimit, cursor, order,
		)
		if err != nil {
			return clusteredJobResp(*resp), err
		}

		endCursor := resp.Organization.Jobs.PageInfo.EndCursor
		clusteredJobs = append(clusteredJobs, resp.Organization.Jobs.Edges...)
		paginationDepth++

		if !resp.Organization.Jobs.PageInfo.HasNextPage || paginationDepth >= m.cfg.PaginationDepthLimit || endCursor == nil {
			break
		}

		cursor = *endCursor
	}

	// Create a combined response
	clusteredResp := &api.GetScheduledJobsClusteredResponse{
		Organization: api.GetScheduledJobsClusteredOrganization{
			Id: resp.Organization.Id,
			Jobs: api.GetScheduledJobsClusteredOrganizationJobsJobConnection{
				Count:    len(clusteredJobs),
				PageInfo: resp.Organization.Jobs.PageInfo,
				Edges:    clusteredJobs,
			},
		},
	}

	return clusteredJobResp(*clusteredResp), err
}

func (m *Monitor) Start(ctx context.Context, handler model.JobHandler) <-chan error {
	logger := m.logger.With(zap.String("org", m.cfg.Org))
	errs := make(chan error, 1)

	agentTags, tagErrs := agenttags.TagMapFromTags(m.cfg.Tags)
	if len(tagErrs) != 0 {
		logger.Warn("making a map of agent tags", zap.Errors("err", tagErrs))
	}

	var queue string
	var ok bool
	if queue, ok = agentTags["queue"]; !ok {
		errs <- errors.New("missing required tag: queue")
		return errs
	}

	go func() {
		logger.Info("started")
		defer logger.Info("stopped")

		monitorUpGauge.Set(1)
		defer monitorUpGauge.Set(0)

		ticker := time.NewTicker(m.cfg.PollInterval)
		defer ticker.Stop()

		first := make(chan struct{}, 1)
		first <- struct{}{}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-first:
			}

			queriedAt := time.Now() // used for end-to-end durations

			resp, err := m.getScheduledCommandJobs(ctx, queue)
			if err != nil {
				// Avoid logging if the context is already closed.
				if ctx.Err() != nil {
					return
				}
				logger.Warn("failed to get scheduled command jobs", zap.Error(err))
				continue
			}

			if !resp.OrganizationExists() {
				errs <- fmt.Errorf("invalid organization: %q", m.cfg.Org)
				return
			}

			jobs := resp.CommandJobs()
			if len(jobs) == 0 {
				continue
			}

			jobsReturnedCounter.Add(float64(len(jobs)))

			// The next handler should be the Limiter (except in some tests).
			// Limiter handles deduplicating jobs before passing to the scheduler.
			m.passJobsToNextHandler(ctx, logger, handler, agentTags, jobs, queriedAt)
		}
	}()

	return errs
}

func (m *Monitor) passJobsToNextHandler(
	ctx context.Context,
	logger *zap.Logger,
	handler model.JobHandler,
	agentTags map[string]string,
	jobs []*api.JobJobTypeCommand,
	queriedAt time.Time,
) {
	// A sneaky way to create a channel that is closed after a duration.
	// Why not pass directly to handler.Handle? Because that might
	// interrupt scheduling a pod, when all we want is to bound the
	// time spent waiting for the limiter.
	staleCtx, staleCancel := context.WithTimeout(ctx, m.cfg.StaleJobDataTimeout)
	defer staleCancel()

	// Why shuffle the jobs? Suppose we sort the jobs to prefer, say, oldest.
	// The first job we'll always try to schedule will then be the oldest, which
	// sounds reasonable. But if that job is not able to be accepted by the
	// cluster for some reason (e.g. there are multiple stack controllers on the
	// same BK queue, and the job is already created by another controller),
	// and the k8s API is slow, then we'll live-lock between grabbing jobs,
	// trying to run the same oldest one, failing, then timing out (staleness).
	// Shuffling increases the odds of making progress.
	rand.Shuffle(len(jobs), func(i, j int) {
		jobs[i], jobs[j] = jobs[j], jobs[i]
	})

	// After shuffling, sort by priority. This negates some of the benefit of
	// shuffling (suppose all the highest priority jobs have difficulty being
	// scheduled).
	slices.SortFunc(jobs, func(a, b *api.JobJobTypeCommand) int {
		// Higher number = higher priority.
		// See https://buildkite.com/docs/pipelines/configure/workflows/managing-priorities
		return cmp.Compare(b.Priority.Number, a.Priority.Number)
	})

	// We also try to get more jobs to the API by processing them in parallel.
	jobsCh := make(chan *api.JobJobTypeCommand)
	defer close(jobsCh)

	var wg sync.WaitGroup
	for range min(m.cfg.JobCreationConcurrency, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobHandlerWorker(ctx, staleCtx, logger, handler, agentTags, queriedAt, jobsCh)
		}()
	}
	defer wg.Wait()

	for i, job := range jobs {
		select {
		case <-ctx.Done():
			return
		case <-staleCtx.Done():
			// Every remaining job is stale.
			staleJobsCounter.Add(float64(len(jobs) - i))
			return
		case jobsCh <- job:
		}
	}
}

func jobHandlerWorker(
	ctx, staleCtx context.Context,
	logger *zap.Logger,
	handler model.JobHandler,
	agentTags map[string]string,
	queriedAt time.Time,
	jobsCh <-chan *api.JobJobTypeCommand,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-staleCtx.Done():
			return
		case j := <-jobsCh:
			if j == nil {
				return
			}
			jobsReachedWorkerCounter.Inc()

			jobTags, tagErrs := agenttags.TagMapFromTags(j.AgentQueryRules)
			if len(tagErrs) != 0 {
				logger.Warn("making a map of job tags", zap.Errors("err", tagErrs))
			}

			// The api returns jobs that match ANY agent tags (the agent query rules)
			// However, we can only acquire jobs that match ALL agent tags
			if !agenttags.JobTagsMatchAgentTags(maps.All(jobTags), agentTags) {
				logger.Debug("skipping job because it did not match all tags", zap.Any("job", j))
				jobsFilteredOutCounter.Inc()
				continue
			}

			job := model.Job{
				CommandJob: &j.CommandJob,
				StaleCh:    staleCtx.Done(),
				QueriedAt:  queriedAt,
			}

			// The next handler should be the deduper (except in some tests).
			// Deduper handles deduplicating jobs before passing to the scheduler.
			logger.Debug("passing job to next handler",
				zap.Stringer("handler", reflect.TypeOf(handler)),
				zap.String("job-uuid", j.Uuid),
			)
			jobHandlerCallsCounter.Inc()
			// The next handler operates under the main ctx, but can optionally
			// use staleCtx.Done() (stored in job) to skip work. (Only Limiter
			// does this.)
			switch err := handler.Handle(ctx, job); {
			case errors.Is(err, model.ErrDuplicateJob):
				// Job wasn't scheduled because it's already scheduled.
				jobHandlerErrorCounter.WithLabelValues("duplicate").Inc()

			case errors.Is(err, model.ErrStaleJob):
				// Job wasn't scheduled because the data has become stale.
				// Staleness is set within this function, so we can return early.
				jobHandlerErrorCounter.WithLabelValues("stale").Inc()
				staleJobsCounter.Inc() // also incremented elsewhere
				return

			case err != nil:
				// Note: this check is for the original context, not staleCtx,
				// in order to avoid the log when the context is cancelled
				// (particularly during tests).
				if ctx.Err() != nil {
					return
				}
				logger.Error("failed to create job", zap.Error(err))
				jobHandlerErrorCounter.WithLabelValues("other").Inc()
			}
		}
	}
}

func encodeClusterGraphQLID(clusterUUID string) string {
	return base64.StdEncoding.EncodeToString([]byte("Cluster---" + clusterUUID))
}
