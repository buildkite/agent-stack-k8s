package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
	"github.com/buildkite/roko"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupOrphanedPipelines(t *testing.T) {
	if !cleanupPipelines {
		t.Skip("not cleaning orphaned pipelines")
	}

	ctx := context.Background()
	graphqlClient := api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint)
	orgSlug := getOrgSlug(t)

	allPipelines, err := searchAllPipelines(ctx, t, graphqlClient, orgSlug, "test-")
	require.NoError(t, err)

	t.Logf("found %d total pipelines to delete", len(allPipelines))

	for _, pipeline := range allPipelines {
		t.Run(pipeline.Node.Name, func(t *testing.T) {
			tc := testcase{
				T:            t,
				GraphQL:      graphqlClient,
				PipelineName: pipeline.Node.Name,
			}.Init()

			builds, err := api.GetBuilds(
				ctx,
				graphqlClient,
				fmt.Sprintf("%s/%s", tc.Org, pipeline.Node.Name),
				[]api.BuildStates{api.BuildStatesRunning},
				100,
			)
			require.NoError(t, err)

			for _, build := range builds.Pipeline.Builds.Edges {
				_, err = api.BuildCancel(
					ctx,
					graphqlClient,
					api.BuildCancelInput{Id: build.Node.Id},
				)
				assert.NoError(t, err)
			}

			tc.deletePipeline(ctx)
		})
	}
}

func (t testcase) deletePipeline(ctx context.Context) {
	t.Helper()

	EnsureCleanup(t.T, func() {
		if err := roko.NewRetrier(
			roko.WithMaxAttempts(10),
			roko.WithStrategy(roko.Exponential(time.Second, 5*time.Second)),
		).DoWithContext(ctx, func(r *roko.Retrier) error {
			resp, err := t.Buildkite.Pipelines.Delete(t.Org, t.PipelineName)
			if err != nil {
				if resp.StatusCode == http.StatusNotFound {
					return nil
				}
				t.Logf("waiting for build to be canceled on pipeline %s", t.PipelineName)
				return err
			}
			return nil
		}); err != nil {
			t.Errorf("failed to cleanup pipeline %s: %v", t.PipelineName, err)
			return
		}

		t.Logf("deleted pipeline! %s", t.PipelineName)
	})
}

func getOrgSlug(t *testing.T) string {
	tc := testcase{
		T: t,
	}.Init()
	return tc.Org
}

// searchAllPipelines fetches all pipelines matching the search prefix, paginating through all results.
func searchAllPipelines(
	ctx context.Context,
	t *testing.T,
	client graphql.Client,
	orgSlug string,
	searchPrefix string,
) ([]api.SearchPipelinesOrganizationPipelinesPipelineConnectionEdgesPipelineEdge, error) {
	var allPipelines []api.SearchPipelinesOrganizationPipelinesPipelineConnectionEdgesPipelineEdge
	var cursor string
	pageNum := 0

	for {
		pageNum++
		pipelines, err := api.SearchPipelines(ctx, client, orgSlug, searchPrefix, 100, cursor)
		if err != nil {
			return nil, err
		}

		edges := pipelines.Organization.Pipelines.Edges
		t.Logf("page %d: found %d pipelines", pageNum, len(edges))
		allPipelines = append(allPipelines, edges...)

		pageInfo := pipelines.Organization.Pipelines.PageInfo
		if !pageInfo.HasNextPage {
			break
		}
		cursor = pageInfo.EndCursor
	}

	return allPipelines, nil
}
