package monitor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

type Monitor struct {
	gql    graphql.Client
	logger *zap.Logger
	cfg    Config
}

type Config struct {
	GraphQLEndpoint     string
	Namespace           string
	Token               string
	ClusterUUID         string
	MaxInFlight         int
	PollInterval        time.Duration
	StaleJobDataTimeout time.Duration
	Org                 string
	Tags                []string
}

type Job struct {
	// The job information.
	*api.CommandJob

	// Closed when the job information becomes stale.
	StaleCh <-chan struct{}
}

type JobHandler interface {
	Handle(context.Context, Job) error
}

func New(logger *zap.Logger, k8s kubernetes.Interface, cfg Config) (*Monitor, error) {
	graphqlClient := api.NewClient(cfg.Token, cfg.GraphQLEndpoint)

	// Poll no more frequently than every 1s (please don't DoS us).
	cfg.PollInterval = min(cfg.PollInterval, time.Second)

	// Default StaleJobDataTimeout to 10s.
	if cfg.StaleJobDataTimeout <= 0 {
		cfg.StaleJobDataTimeout = 10 * time.Second
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
func (m *Monitor) getScheduledCommandJobs(ctx context.Context, queue string) (jobResp, error) {
	if m.cfg.ClusterUUID == "" {
		resp, err := api.GetScheduledJobs(ctx, m.gql, m.cfg.Org, []string{fmt.Sprintf("queue=%s", queue)})
		return unclusteredJobResp(*resp), err
	}

	var agentQueryRule []string
	if queue != "" {
		agentQueryRule = append(agentQueryRule, fmt.Sprintf("queue=%s", queue))
	}

	resp, err := api.GetScheduledJobsClustered(
		ctx, m.gql, m.cfg.Org, agentQueryRule, encodeClusterGraphQLID(m.cfg.ClusterUUID),
	)
	return clusteredJobResp(*resp), err
}

func toMapAndLogErrors(logger *zap.Logger, tags []string) map[string]string {
	agentTags, tagErrs := agenttags.ToMap(tags)
	if len(tagErrs) != 0 {
		logger.Warn("making a map of agent tags", zap.Errors("err", tagErrs))
	}
	return agentTags
}

func (m *Monitor) Start(ctx context.Context, handler JobHandler) <-chan error {
	logger := m.logger.With(zap.String("org", m.cfg.Org))
	errs := make(chan error, 1)

	agentTags := toMapAndLogErrors(logger, m.cfg.Tags)

	var queue string
	var ok bool
	if queue, ok = agentTags["queue"]; !ok {
		errs <- errors.New("missing required tag: queue")
		return errs
	}

	go func() {
		logger.Info("started")
		defer logger.Info("stopped")

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

			// The next handler should be the Limiter (except in some tests).
			// Limiter handles deduplicating jobs before passing to the scheduler.
			m.passJobsToNextHandler(ctx, logger, handler, agentTags, resp.CommandJobs())
		}
	}()

	return errs
}

func (m *Monitor) passJobsToNextHandler(ctx context.Context, logger *zap.Logger, handler JobHandler, agentTags map[string]string, jobs []*api.JobJobTypeCommand) {
	// A sneaky way to create a channel that is closed after a duration.
	// Why not pass directly to handler.Create? Because that might
	// interrupt scheduling a pod, when all we want is to bound the
	// time spent waiting for the limiter.
	staleCtx, staleCancel := context.WithTimeout(ctx, m.cfg.StaleJobDataTimeout)
	defer staleCancel()

	// TODO: sort by ScheduledAt in the API
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].ScheduledAt.Before(jobs[j].ScheduledAt)
	})

	for _, j := range jobs {
		if staleCtx.Err() != nil {
			// Results already stale; try again later.
			return
		}

		jobTags := toMapAndLogErrors(logger, j.AgentQueryRules)

		// The api returns jobs that match ANY agent tags (the agent query rules)
		// However, we can only acquire jobs that match ALL agent tags
		if !agenttags.JobTagsMatchAgentTags(jobTags, agentTags) {
			logger.Debug("skipping job because it did not match all tags", zap.Any("job", j))
			continue
		}

		job := Job{
			CommandJob: &j.CommandJob,
			StaleCh:    staleCtx.Done(),
		}

		// The next handler should be the Limiter (except in some tests).
		// Limiter handles deduplicating jobs before passing to the scheduler.
		logger.Debug("passing job to next handler",
			zap.Stringer("handler", reflect.TypeOf(handler)),
			zap.String("uuid", j.Uuid),
		)
		if err := handler.Handle(ctx, job); err != nil {
			// Note: this check is for the original context, not staleCtx.
			if ctx.Err() != nil {
				return
			}
			logger.Error("failed to create job", zap.Error(err))
		}
	}
}

func encodeClusterGraphQLID(clusterUUID string) string {
	return base64.StdEncoding.EncodeToString([]byte("Cluster---" + clusterUUID))
}
