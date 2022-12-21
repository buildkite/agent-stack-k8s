package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

type Monitor struct {
	ctx    context.Context
	gql    graphql.Client
	k8s    *api.BuildkiteJobManager
	logger *zap.Logger
	cfg    Config
	once   sync.Once
	jobs   chan Job
}

type Config struct {
	Namespace   string
	Token       string
	MaxInFlight int32
	Org         string
}

type Job struct {
	api.CommandJob
	Err error
	Tag string
}

func New(ctx context.Context, logger *zap.Logger, jobManager *api.BuildkiteJobManager, cfg Config) *Monitor {
	graphqlClient := api.NewClient(cfg.Token)

	return &Monitor{
		ctx:    ctx,
		gql:    graphqlClient,
		k8s:    jobManager,
		logger: logger,
		cfg:    cfg,
		jobs:   make(chan Job),
	}
}

func (m *Monitor) Scheduled() <-chan Job {
	go m.once.Do(func() { go m.start() })
	return m.jobs
}

func (m *Monitor) start() {
	m.logger.Info("started", zap.String("org", m.cfg.Org), zap.String("namespace", m.cfg.Namespace), zap.Int32("max-in-flight", m.cfg.MaxInFlight))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.reachedMaxInFlight() {
				m.logger.Info("max in flight reached", zap.Int32("in-flight", m.cfg.MaxInFlight))
				continue
			}
		Out:
			for _, tag := range m.k8s.Tags {
				buildsResponse, err := api.GetScheduledBuilds(m.ctx, m.gql, m.cfg.Org, []string{tag})
				if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					m.logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
					continue
				}
				if buildsResponse.Organization.Id == nil {
					m.jobs <- Job{Err: fmt.Errorf("invalid organization: %s", m.cfg.Org)}
				}
				builds := buildsResponse.Organization.Jobs.Edges
				sort.Slice(builds, func(i, j int) bool {
					cmdI := builds[i].Node.(*api.JobJobTypeCommand)
					cmdJ := builds[j].Node.(*api.JobJobTypeCommand)

					return cmdI.ScheduledAt.Before(cmdJ.ScheduledAt)

				})

				for _, job := range builds {
					cmdJob := job.Node.(*api.JobJobTypeCommand)
					err := m.scheduleBuild(cmdJob, tag)
					if errors.Is(err, &reachedMaxInFlight{}) {
						break Out
					}
				}
			}
		}
	}
}

func (m *Monitor) scheduleBuild(cmdJob *api.JobJobTypeCommand, tag string) error {
	if m.isJobInFlight(cmdJob.Uuid) {
		m.logger.Debug("skipping already queued job", zap.String("uuid", cmdJob.Uuid))
	} else if m.reachedMaxInFlight() {
		m.logger.Debug("max in flight reached", zap.Int32("in-flight", m.cfg.MaxInFlight))
		return &reachedMaxInFlight{}
	} else {
		m.jobs <- Job{
			CommandJob: cmdJob.CommandJob,
			Tag:        tag,
		}
		m.logger.Info("added job", zap.String("uuid", cmdJob.Uuid))
	}
	return nil
}

func (m *Monitor) isJobInFlight(uuid string) bool {
	req, err := labels.NewRequirement(api.UUIDLabel, selection.Equals, []string{uuid})
	if err != nil {
		m.logger.Error(fmt.Sprintf("Failed to build label selector for job in flight flight: %s", err))
		return false
	}
	selector := labels.NewSelector()
	selector = selector.Add(*req)
	jobsResp, err := m.k8s.JobLister.List(selector)
	if err != nil {
		m.logger.Error(fmt.Sprintf("Failed to query for job in flight: %s", err))
		return false
	}
	return len(jobsResp) > 0
}

func isComplete(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Status == corev1.ConditionTrue {
			if condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed {
				return true
			}
		}
	}
	return false
}

func (m *Monitor) reachedMaxInFlight() bool {
	if m.cfg.MaxInFlight == 0 {
		return false
	}
	var activeJobs int32
	jobList, err := m.k8s.JobLister.List(labels.Everything())
	if err != nil {
		m.logger.Error(fmt.Sprintf("Unable to list active jobs: %s", err))
		return true
	}
	for _, job := range jobList {
		if job.Status.Active != 0 {
			activeJobs++
		}
	}
	return activeJobs >= m.cfg.MaxInFlight
}

type reachedMaxInFlight struct{}

func (*reachedMaxInFlight) Error() string {
	return "not scheduling job because max in flight"
}
