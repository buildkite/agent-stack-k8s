package monitor

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/agenttags"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/model"

	"log/slog"
)

// Monitor is responsible for polling Buildkite for jobs.
type Monitor struct {
	agentClient *api.AgentClient
	logger      *slog.Logger
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
	Tags                 []string          // same as TagMap but in k=v form
	TagMap               map[string]string // same as Tags but in map form
	EnableQueuePause     bool
	PaginationPageSize   int // max size of each page
	PaginationDepthLimit int // max count of pages to query
	QueryResetInterval   time.Duration
}

func New(logger *slog.Logger, agentClient *api.AgentClient, cfg Config) (*Monitor, error) {
	// Poll no more frequently than every 1s (please don't DoS us).
	cfg.PollInterval = max(cfg.PollInterval, time.Second)

	return &Monitor{
		agentClient: agentClient,
		logger:      logger,
		cfg:         cfg,
	}, nil
}

// getScheduledCommandJobs gets scheduled jobs from the API.
func (m *Monitor) getScheduledCommandJobs(ctx context.Context) (jobs []*api.AgentScheduledJob, retryAfter time.Duration, err error) {
	m.logger.Debug("retrieving scheduled jobs via Agent API...", "poll-interval", m.cfg.PollInterval)

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
		resp, ra, err := m.agentClient.GetScheduledJobs(ctx, m.cursor, m.cfg.PaginationPageSize)
		if err != nil {
			// Reset the cursor for next time.
			m.cursor = ""
			return nil, ra, err
		}
		// The query was successful, update the cursor to be the ID of the last job
		// in the response (if there are any).
		m.cursor = resp.PageInfo.EndCursor
		jobs = append(jobs, resp.Jobs...)
		// No error, but Retry-After was set to something, so do the next query
		// later.
		if ra > 0 {
			return jobs, ra, nil
		}
		if !resp.PageInfo.HasNextPage {
			break
		}
	}
	return jobs, 0, nil
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

		retryAfterCh := time.After(0)

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				// continue below

			case <-first:
				// continue below
			}

			// Also wait for retryAfter, if set
			select {
			case <-ctx.Done():
				return

			case <-retryAfterCh:
				// continue below
			}

			jobs, retryAfter, err := m.getScheduledCommandJobs(ctx)
			if api.IsPermanentError(err) { // it's not temporary!
				errs <- fmt.Errorf("while fetching scheduled jobs: %w", err)
				return
			}
			// Whether there's an error or not, respect the Retry-After header.
			// If Retry-After is not present, retryAfter <= 0, so
			// time.After(retryAfter) can be read from immediately.
			retryAfterCh = time.After(retryAfter)

			if err != nil {
				// Avoid logging if the context is already closed.
				if ctx.Err() != nil {
					return
				}
				m.logger.Warn("failed to get scheduled command jobs", "error", err)
				continue
			}

			level := slog.LevelDebug
			if len(jobs) > 0 {
				level = slog.LevelInfo
			}
			m.logger.Log(ctx, level, "job processing of Agent API results completed", "agent-api-jobs-processed", len(jobs))

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
		// Convert the job tags to a map.
		jobTags, tagErrs := agenttags.TagMapFromTags(job.AgentQueryRules)
		if len(tagErrs) != 0 {
			m.logger.Warn("making a map of job tags", "err", tagErrs)
		}

		// The API returns all jobs within the queue, with any combo of tags.
		// However, we should only acquire jobs that match ALL agent tags.
		if !agenttags.MatchJobTags(m.cfg.TagMap, jobTags) {
			jobsFilteredOutCounter.Inc()
			m.logger.Info("job tags do not match expected tags in configuration, skipping...",
				"job-uuid", job.ID,
				"controller-tags", m.cfg.TagMap,
				"buildkite-job-tags", jobTags,
			)
			continue
		}

		filteredJobs = append(filteredJobs, job)
	}

	// The next handler should be the limiter, which handles batches.
	m.logger.Debug("passing jobs to next handler",
		"handler", reflect.TypeOf(handler),
		"job-count", len(filteredJobs),
	)
	jobHandlerCallsCounter.Inc()
	jobsHandledCounter.Add(float64(len(filteredJobs)))

	if err := handler.HandleMany(ctx, filteredJobs); err != nil {
		if ctx.Err() != nil {
			return
		}
		m.logger.Error("failed to create jobs", "error", err)
		jobHandlerErrorCounter.Inc()
		jobsHandledErrorsCounter.Add(float64(len(filteredJobs)))
	}
}
