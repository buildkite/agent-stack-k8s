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
	"k8s.io/client-go/kubernetes"
)

type Monitor struct {
	ctx    context.Context
	gql    graphql.Client
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

	return &Monitor{
		ctx:    ctx,
		gql:    graphqlClient,
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
					m.scheduleBuild(cmdJob, tag)
				}
			}
		}
	}
}

func (m *Monitor) scheduleBuild(cmdJob *api.JobJobTypeCommand, tag string) {
	m.logger.Info("queuing job", zap.String("uuid", cmdJob.Uuid))
	m.jobs <- Job{
		CommandJob: cmdJob.CommandJob,
		Tag:        tag,
	}
}
