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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	batchlisters "k8s.io/client-go/listers/batch/v1"
)

type Monitor struct {
	ctx    context.Context
	gql    graphql.Client
	k8s    batchlisters.JobLister
	logger *zap.Logger
	cfg    api.Config
	once   sync.Once
	jobs   chan Job
}

type Config struct {
	Namespace   string
	Token       string
	MaxInFlight int
	Org         string
	Tags        []string
}

type Job struct {
	api.CommandJob
	Err error
	Tag string
}

func New(ctx context.Context, logger *zap.Logger, k8s kubernetes.Interface, cfg api.Config) (*Monitor, error) {
	graphqlClient := api.NewClient(cfg.BuildkiteToken)
	jobLister, err := NewJobLister(ctx, logger.Named("lister"), k8s, cfg.Tags)
	if err != nil {
		return nil, err
	}

	return &Monitor{
		ctx:    ctx,
		gql:    graphqlClient,
		k8s:    jobLister,
		logger: logger,
		cfg:    cfg,
		jobs:   make(chan Job),
	}, nil
}

func (m *Monitor) Scheduled() <-chan Job {
	go m.once.Do(func() { go m.start() })
	return m.jobs
}

func (m *Monitor) start() {
	m.logger.Info("started")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.reachedMaxInFlight() {
				m.logger.Info("max in flight reached", zap.Int("in-flight", m.cfg.MaxInFlight))
				continue
			}
		Out:
			for _, tag := range m.cfg.Tags {
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
		m.logger.Debug("max in flight reached", zap.Int("in-flight", m.cfg.MaxInFlight))
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
	jobsResp, err := m.k8s.List(selector)
	if err != nil {
		m.logger.Error(fmt.Sprintf("Failed to query for job in flight: %s", err))
		return false
	}
	return len(jobsResp) > 0
}

func (m *Monitor) reachedMaxInFlight() bool {
	if m.cfg.MaxInFlight == 0 {
		return false
	}
	var activeJobs int
	jobList, err := m.k8s.List(labels.Everything())
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
