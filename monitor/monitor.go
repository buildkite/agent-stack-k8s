package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	"go.uber.org/zap"
)

type Monitor struct {
	client      graphql.Client
	logger      *zap.Logger
	knownBuilds map[string]struct{}
}

func New(logger *zap.Logger, token string) *Monitor {
	graphqlClient := api.NewClient(token)
	return &Monitor{
		client:      graphqlClient,
		logger:      logger,
		knownBuilds: map[string]struct{}{},
	}
}

func (m *Monitor) Watch(ctx context.Context, org, pipeline string) <-chan api.CommandJob {
	jobs := make(chan api.CommandJob)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.logger.Info("monitor context canceled")
				return
			case <-ticker.C:
				slug := fmt.Sprintf("%s/%s", org, pipeline)
				buildsResponse, err := api.GetBuildsForPipelineBySlug(ctx, m.client, slug)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					m.logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
					continue
				}
				for _, job := range buildsResponse.Pipeline.Jobs.Edges {
					switch job := job.Node.(type) {
					case *api.GetBuildsForPipelineBySlugPipelineJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand:
						if _, found := m.knownBuilds[job.Uuid]; found {
							m.logger.Debug("skipping known job", zap.String("uuid", job.Uuid))
						} else {
							m.logger.Debug("enqueuing job", zap.String("uuid", job.Uuid))
							jobs <- job.CommandJob
							m.knownBuilds[job.Uuid] = struct{}{}
						}
					default:
						m.logger.Warn("received unknown job type", zap.Any("type", job.GetTypename()))
						continue
					}
				}
			}
		}
	}()
	return jobs
}
