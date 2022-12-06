package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

type Monitor struct {
	client      graphql.Client
	logger      *zap.Logger
	knownBuilds *lru.Cache[string, struct{}]
	maxInFlight int
}

func New(logger *zap.Logger, token string, maxInFlight int) (*Monitor, error) {
	graphqlClient := api.NewClient(token)
	cache, err := lru.New[string, struct{}](100)
	if err != nil {
		return nil, err
	}
	return &Monitor{
		client:      graphqlClient,
		logger:      logger,
		knownBuilds: cache,
		maxInFlight: maxInFlight,
	}, nil
}

func (m *Monitor) Scheduled(ctx context.Context, org, pipeline string) <-chan api.CommandJob {
	jobs := make(chan api.CommandJob)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				slug := fmt.Sprintf("%s/%s", org, pipeline)
				buildsResponse, err := api.GetScheduledBuilds(ctx, m.client, slug)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					m.logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
					continue
				}
				builds := buildsResponse.Pipeline.Jobs.Edges
				sort.Slice(builds, func(i, j int) bool {
					cmdI := builds[i].Node.(*api.JobJobTypeCommand)
					cmdJ := builds[j].Node.(*api.JobJobTypeCommand)

					return cmdI.ScheduledAt.Before(cmdJ.ScheduledAt)

				})

				for _, job := range builds {
					cmdJob := job.Node.(*api.JobJobTypeCommand)
					if !m.knownBuilds.Contains(cmdJob.Uuid) && m.knownBuilds.Len() < m.maxInFlight {
						jobs <- cmdJob.CommandJob
						m.logger.Debug("added job", zap.String("uuid", cmdJob.Uuid))
						m.knownBuilds.Add(cmdJob.Uuid, struct{}{})
					}
				}
			}
		}
	}()
	return jobs
}

func (m *Monitor) Done(uuid string) {
	m.knownBuilds.Remove(uuid)
}
