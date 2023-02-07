package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

type Monitor struct {
	gql    graphql.Client
	logger *zap.Logger
	cfg    api.Config
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
	Tag string
}

type JobHandler interface {
	Create(context.Context, *Job) error
}

func New(logger *zap.Logger, k8s kubernetes.Interface, cfg api.Config) (*Monitor, error) {
	graphqlClient := api.NewClient(cfg.BuildkiteToken)

	return &Monitor{
		gql:    graphqlClient,
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (m *Monitor) Start(ctx context.Context, handler JobHandler) <-chan error {
	errs := make(chan error, 1)
	go func() {
		m.logger.Info("started")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			for _, tag := range m.cfg.Tags {
				buildsResponse, err := api.GetScheduledBuilds(ctx, m.gql, m.cfg.Org, []string{tag})
				if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					m.logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
					continue
				}
				if buildsResponse.Organization.Id == nil {
					errs <- fmt.Errorf("invalid organization: %s", m.cfg.Org)
					return
				}
				builds := buildsResponse.Organization.Jobs.Edges
				sort.Slice(builds, func(i, j int) bool {
					cmdI := builds[i].Node.(*api.JobJobTypeCommand)
					cmdJ := builds[j].Node.(*api.JobJobTypeCommand)

					return cmdI.ScheduledAt.Before(cmdJ.ScheduledAt)
				})

				for _, job := range builds {
					cmdJob := job.Node.(*api.JobJobTypeCommand)
					m.logger.Debug("creating job", zap.String("uuid", cmdJob.Uuid))
					handler.Create(ctx, &Job{
						CommandJob: cmdJob.CommandJob,
						Tag:        tag,
					})
				}
			}

			select {
			case <-ctx.Done():
				close(errs)
				return
			case <-ticker.C:
				continue
			}
		}
	}()
	return errs
}
