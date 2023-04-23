package monitor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

type Cluster struct {
	UUID      string
	GraphQLID string
}

func (c Cluster) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("uuid", c.UUID)
	enc.AddString("graphql-id", c.GraphQLID)
	return nil
}

func New(logger *zap.Logger, k8s kubernetes.Interface, cfg api.Config) (*Monitor, error) {
	graphqlClient := api.NewClient(cfg.BuildkiteToken)

	return &Monitor{
		gql:    graphqlClient,
		logger: logger,
		cfg:    cfg,
	}, nil
}

func (m *Monitor) getScheduledCommandJobs(
	ctx context.Context,
	tag string,
) (
	jobs []*api.JobJobTypeCommand,
	fatal bool,
	err error,
) {
	logger := m.logger
	if m.cfg.ClusterUUID == "" {
		jobsResp, err := api.GetScheduledJobs(ctx, m.gql, m.cfg.Org, []string{tag})
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
			}
			return nil, false, err
		}

		if jobsResp.Organization.Id == nil {
			return nil, true, fmt.Errorf("invalid organization: %s", m.cfg.Org)
		}

		jobs = make([]*api.JobJobTypeCommand, 0, len(jobsResp.Organization.Jobs.Edges))
		for _, job := range jobsResp.Organization.Jobs.Edges {
			jobs = append(jobs, job.Node.(*api.JobJobTypeCommand))
		}
	} else {
		clusterGraphQLID := encodeClusterGraphQLID(m.cfg.ClusterUUID)
		logger := logger.With(zap.Object("cluster", Cluster{UUID: m.cfg.ClusterUUID, GraphQLID: clusterGraphQLID}))

		jobsResp, err := api.GetScheduledJobsClustered(ctx, m.gql, m.cfg.Org, []string{tag}, clusterGraphQLID)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
			}
			return nil, false, err
		}

		if jobsResp.Organization.Id == nil {
			return nil, true, fmt.Errorf("invalid organization: %s", m.cfg.Org)
		}

		jobs = make([]*api.JobJobTypeCommand, 0, len(jobsResp.Organization.Jobs.Edges))
		for _, job := range jobsResp.Organization.Jobs.Edges {
			jobs = append(jobs, job.Node.(*api.JobJobTypeCommand))
		}
	}

	return jobs, false, nil
}

func (m *Monitor) Start(ctx context.Context, handler JobHandler) <-chan error {
	logger := m.logger.With(zap.String("org", m.cfg.Org))
	errs := make(chan error, 1)
	go func() {
		m.logger.Info("started")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			for _, tag := range m.cfg.Tags {
				jobs, fatal, err := m.getScheduledCommandJobs(ctx, tag)
				if err != nil {
					if fatal {
						errs <- err
						return
					}
					logger.Warn("failed to get scheduled command jobs", zap.Error(err))
					continue
				}

				// TODO: sort by ScheduledAt in the API
				sort.Slice(jobs, func(i, j int) bool {
					return jobs[i].ScheduledAt.Before(jobs[j].ScheduledAt)
				})

				for _, job := range jobs {
					logger.Debug("creating job", zap.String("uuid", job.Uuid))
					if err := handler.Create(ctx, &Job{
						CommandJob: job.CommandJob,
						Tag:        tag,
					}); err != nil {
						logger.Error("failed to create job", zap.Error(err))
					}
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

func encodeClusterGraphQLID(clusterUUID string) string {
	return base64.StdEncoding.EncodeToString([]byte("Cluster---" + clusterUUID))
}
