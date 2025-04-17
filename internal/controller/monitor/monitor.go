package monitor

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

// Monitor is responsible for polling Buildkite for jobs.
type Monitor struct {
	agentClient *api.AgentClient
	logger      *zap.Logger
	cfg         Config
	cursor      string // for the next GetScheduledJobs
	lastQuery   time.Time
}

type Config struct {
	Namespace            string
	ClusterUUID          string
	Queue                string
	MaxInFlight          int
	PollInterval         time.Duration
	Org                  string
	Tags                 []string          // same as TagMap but in k=v form
	TagMap               map[string]string // same as Tags but in map form
	EnableQueuePause     bool
	PaginationPageSize   int // max size of each page
	PaginationDepthLimit int // max count of pages to query
	QueryResetInterval   time.Duration
}

func New(logger *zap.Logger, k8s kubernetes.Interface, agentClient *api.AgentClient, cfg Config) (*Monitor, error) {
	logger = logger.With(zap.String("org", cfg.Org))

	// Poll no more frequently than every 1s (please don't DoS us).
	cfg.PollInterval = max(cfg.PollInterval, time.Second)

	return &Monitor{
		agentClient: agentClient,
		logger:      logger,
		cfg:         cfg,
	}, nil
}

// getScheduledCommandJobs gets scheduled jobs from the API.
func (m *Monitor) getScheduledCommandJobs(ctx context.Context) (jobs []*api.AgentScheduledJob, err error) {
	m.logger.Info("retrieving scheduled jobs via Agent API...",
		zap.Duration("poll-interval", m.cfg.PollInterval),
	)

	jobQueryCounter.Inc()
	start := time.Now()
	defer func() {
		jobQueryDurationHistogram.Observe(time.Since(start).Seconds())
		if err != nil {
			jobQueryErrorCounter.Inc()
		}
	}()

	// Because of eventual consistency, continuing to query only for new jobs
	// means that some jobs could be missed. Every so often, restart from the
	// beginning.
	if start.Sub(m.lastQuery) > m.cfg.QueryResetInterval {
		m.cursor = ""
	}
	m.lastQuery = start

	// Repeat the paginated query.
	for range m.cfg.PaginationDepthLimit {
		// Get all jobs in the (org,cluster,queue) since m.lastScheduledAt.
		// There's no retry loop because the monitor queries this once every
		// cfg.PollInterval (default 1 sec).
		resp, err := m.agentClient.GetScheduledJobs(ctx, m.cursor, m.cfg.PaginationPageSize)
		if err != nil {
			// Reset the cursor for next time.
			m.cursor = ""
			return nil, err
		}
		// The query was successful, update the cursor to be the ID of the last job
		// in the response (if there are any).
		m.cursor = resp.PageInfo.EndCursor
		jobs = append(jobs, resp.Jobs...)
		if !resp.PageInfo.HasNextPage {
			break
		}
	}
	return jobs, nil
}

func (m *Monitor) Start(ctx context.Context, handler model.ManyJobHandler) <-chan error {
	errs := make(chan error, 1)

	go func() {
		m.logger.Info("started")
		defer m.logger.Info("stopped")

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

			jobs, err := m.getScheduledCommandJobs(ctx)
			if api.IsPermanentError(err) { // it's not temporary!
				errs <- fmt.Errorf("while fetching scheduled jobs: %w", err)
				return
			}
			if err != nil {
				// Avoid logging if the context is already closed.
				if ctx.Err() != nil {
					return
				}
				m.logger.Warn("failed to get scheduled command jobs", zap.Error(err))
				continue
			}

			m.logger.Info("job processing of Agent API results completed...",
				zap.Int("agent-api-jobs-processed", len(jobs)),
			)

			if len(jobs) == 0 {
				continue
			}

			jobsReturnedCounter.Add(float64(len(jobs)))

			m.passJobsToNextHandler(ctx, handler, jobs)
		}
	}()

	return errs
}

func (m *Monitor) passJobsToNextHandler(
	ctx context.Context,
	handler model.ManyJobHandler,
	jobs []*api.AgentScheduledJob,
) {
	filteredJobs := make([]*api.AgentScheduledJob, 0, len(jobs))

	for _, job := range jobs {
		jobTags, tagErrs := agenttags.TagMapFromTags(job.AgentQueryRules)
		if len(tagErrs) != 0 {
			m.logger.Warn("making a map of job tags", zap.Errors("err", tagErrs))
		}

		// The API returns jobs that match ANY agent tags (the agent query rules)
		// (howevber we do know it is specific to the configured queue).
		// However, we can only acquire jobs that match ALL agent tags
		if !agenttags.JobTagsMatchAgentTags(jobTags, m.cfg.TagMap) {
			jobsFilteredOutCounter.Inc()
			m.logger.Info("job tags do not match expected tags in configuration, skipping...",
				zap.String("job-uuid", job.ID),
				zap.Any("controller-tags", m.cfg.TagMap),
				zap.Any("buildkite-job-tags", jobTags),
			)
			continue
		}

		// Now that we know the tags match, ensure the job has the queue tag.
		job.AgentQueryRules = agenttags.SetTag(job.AgentQueryRules, "queue", m.cfg.Queue)

		filteredJobs = append(filteredJobs, job)
	}

	// The next handler should be the limiter, which handles batches.
	m.logger.Debug("passing jobs to next handler",
		zap.Stringer("handler", reflect.TypeOf(handler)),
		zap.Int("job-count", len(filteredJobs)),
	)
	jobHandlerCallsCounter.Inc()

	if err := handler.HandleMany(ctx, filteredJobs); err != nil {
		if ctx.Err() != nil {
			return
		}
		m.logger.Error("failed to create jobs", zap.Error(err))
		jobHandlerErrorCounter.Inc()
	}
}
