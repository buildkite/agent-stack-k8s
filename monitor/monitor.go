package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/roko"
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
		for {
			select {
			case <-ctx.Done():
				m.logger.Info("monitor context canceled")
				return
			default:
				roko.NewRetrier(
					roko.TryForever(),
					roko.WithStrategy(roko.Constant(time.Second*5)),
				).DoWithContext(ctx, func(r *roko.Retrier) error {
					slug := fmt.Sprintf("%s/%s", org, pipeline)
					buildsResponse, err := api.GetBuildsForPipelineBySlug(ctx, m.client, slug)
					if err != nil {
						m.logger.Warn("failed to retrieve builds for pipeline", zap.Int("attempts", r.AttemptCount()))
						return fmt.Errorf("failed to fetch builds for pipeline: %w", err)
					}
					for _, job := range buildsResponse.Pipeline.Jobs.Edges {
						switch job := job.Node.(type) {
						case *api.GetBuildsForPipelineBySlugPipelineJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand:
							if _, found := m.knownBuilds[job.Uuid]; found {
								m.logger.Debug("found existing build for job", zap.String("uuid", job.Uuid))
							} else {
								jobs <- job.CommandJob
								m.knownBuilds[job.Uuid] = struct{}{}
							}
						default:
							m.logger.Warn("failed to retrieve builds for pipeline", zap.Int("attempts", r.AttemptCount()), zap.Any("job", job))
							return fmt.Errorf("received unknown job type")
						}
					}
					return nil
				})
			}
			time.Sleep(time.Second)
		}
	}()
	return jobs
}
