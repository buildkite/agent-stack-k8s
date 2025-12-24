package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/buildkite/roko"
)

func TestCleanupOrphanedResources(t *testing.T) {
	if !cleanupPipelines {
		t.Skip("not cleaning orphaned pipelines")
	}

	ctx := context.Background()
	graphqlClient := api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint)
	tc := testcase{T: t}.Init()
	orgSlug := tc.Org
	clusterUUID := tc.ClusterUUID

	allPipelines, err := searchAllPipelines(ctx, t, graphqlClient, orgSlug, "test-")
	if err != nil {
		t.Fatalf("failed to search for pipelines: %v", err)
	}

	t.Logf("found %d total pipelines to delete", len(allPipelines))

	bkClient, err := buildkite.NewOpts(buildkite.WithTokenAuth(cfg.BuildkiteToken))
	if err != nil {
		t.Fatalf("failed to create buildkite client: %v", err)
	}

	// First, go through all the pipelines and cancel any running builds they might have
	wg := sync.WaitGroup{}
	for _, pipeline := range allPipelines {
		wg.Go(func() {
			cancelAllBuildsForPipeline(ctx, t, graphqlClient, orgSlug, pipeline.Node.Name)

			// Then, delete the pipeline
			deletePipeline(ctx, t, bkClient, orgSlug, pipeline.Node.Name)
		})
	}

	wg.Wait()

	// Now that all the pipelines have been deleted, go through and delete all the queues that are hanging around
	queues, _, err := bkClient.ClusterQueues.List(ctx, orgSlug, clusterUUID, nil)
	if err != nil {
		t.Fatalf("failed to list queues: %v", err)
	}

	testQueues := make([]buildkite.ClusterQueue, 0, len(queues))
	for _, queue := range queues {
		if strings.HasPrefix(queue.Key, "test-") {
			testQueues = append(testQueues, queue)
		}
	}

	t.Logf("found %d test queues to clean up", len(testQueues))

	for _, queue := range testQueues {
		wg.Go(func() {
			_, err := bkClient.ClusterQueues.Delete(ctx, orgSlug, clusterUUID, queue.ID)
			if err != nil {
				t.Errorf("failed to delete queue %q (%s): %v", queue.Key, queue.ID, err)
				return
			}

			t.Logf("deleted queue %q", queue.Key)
		})
	}

	wg.Wait()
}

func cancelAllBuildsForPipeline(ctx context.Context, t *testing.T, graphqlClient graphql.Client, orgSlug, pipelineSlug string) {
	t.Helper()

	builds, err := api.GetBuilds(ctx, graphqlClient, fmt.Sprintf("%s/%s", orgSlug, pipelineSlug), []api.BuildStates{api.BuildStatesRunning}, 100)
	if err != nil {
		t.Errorf("failed to get builds for pipeline %q while attempting to delete it: %v", pipelineSlug, err)
		return
	}

	t.Logf("Found %d builds to cancel for pipeline %q", len(builds.Pipeline.Builds.Edges), pipelineSlug)

	wg := sync.WaitGroup{}
	for _, build := range builds.Pipeline.Builds.Edges {
		wg.Go(func() {
			_, err = api.BuildCancel(ctx, graphqlClient, api.BuildCancelInput{Id: build.Node.Id})
			if err != nil {
				t.Errorf("failed to cancel build %q while attempting to delete pipeline %q: %v", build.Node.Id, pipelineSlug, err)
				return
			}

			t.Logf("cancelled build %q", build.Node.Id)
		})
	}

	wg.Wait()
}

func deletePipeline(ctx context.Context, t *testing.T, apiClient *buildkite.Client, orgSlug, pipelineSlug string) {
	t.Helper()

	if err := roko.NewRetrier(
		roko.WithMaxAttempts(10),
		roko.WithStrategy(roko.Exponential(time.Second, 5*time.Second)),
	).DoWithContext(ctx, func(r *roko.Retrier) error {
		resp, err := apiClient.Pipelines.Delete(ctx, orgSlug, pipelineSlug)
		if err != nil {
			if resp.StatusCode == http.StatusNotFound {
				return nil
			}

			return err
		}

		return nil
	}); err != nil {
		t.Errorf("failed to delete pipeline %q: %v", pipelineSlug, err)
		return
	}

	t.Logf("deleted pipeline %q", pipelineSlug)
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
