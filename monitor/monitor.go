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
				builds := buildsResponse.Pipeline.Jobs.Edges
				sort.Slice(builds, func(i, j int) bool {
					cmdI := builds[i].Node.(*api.GetBuildsForPipelineBySlugPipelineJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand)
					cmdJ := builds[j].Node.(*api.GetBuildsForPipelineBySlugPipelineJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand)

					return cmdI.ScheduledAt.Before(cmdJ.ScheduledAt)

				})
				for _, job := range builds {
					cmdJob := job.Node.(*api.GetBuildsForPipelineBySlugPipelineJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand)
					if _, found := m.knownBuilds[cmdJob.Uuid]; !found {
						m.logger.Debug("enqueuing job", zap.String("uuid", cmdJob.Uuid))
						jobs <- cmdJob.CommandJob
						m.knownBuilds[cmdJob.Uuid] = struct{}{}
					}
				}
			}
		}
	}()
	return jobs
}
