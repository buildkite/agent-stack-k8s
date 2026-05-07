package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
	"github.com/buildkite/roko"
)

func TestCleanupOrphanedPipelines(t *testing.T) {
	if !cleanupPipelines {
		t.Skip("not cleaning orphaned pipelines")
	}

	ctx := context.Background()
	graphqlClient := api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint)

	pipelines, err := api.SearchPipelines(ctx, graphqlClient, getOrgSlug(t), "test-", 100)
	if err != nil {
		t.Fatalf("api.SearchPipelines(ctx, graphqlClient, %q, %q, %d) error = %v, want nil", getOrgSlug(t), "test-", 100, err)
	}

	numPipelines := len(pipelines.Organization.Pipelines.Edges)
	t.Logf("found %d pipelines to delete", numPipelines)

	var wg sync.WaitGroup
	wg.Add(numPipelines)
	for _, pipeline := range pipelines.Organization.Pipelines.Edges {
		tc := testcase{
			T:            t,
			GraphQL:      api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
			PipelineName: pipeline.Node.Name,
		}.Init()

		t.Run(pipeline.Node.Name, func(t *testing.T) {
			builds, err := api.GetBuilds(
				ctx,
				graphqlClient,
				fmt.Sprintf("%s/%s", tc.Org, pipeline.Node.Name),
				[]api.BuildStates{api.BuildStatesRunning},
				100,
			)
			if err != nil {
				t.Fatalf("api.GetBuilds(ctx, graphqlClient, %q, []api.BuildStates{api.BuildStatesRunning}, %d) error = %v, want nil", fmt.Sprintf("%s/%s", tc.Org, pipeline.Node.Name), 100, err)
			}

			for _, build := range builds.Pipeline.Builds.Edges {
				_, err = api.BuildCancel(
					ctx,
					graphqlClient,
					api.BuildCancelInput{Id: build.Node.Id},
				)
				if err != nil {
					t.Errorf("api.BuildCancel(ctx, graphqlClient, api.BuildCancelInput{Id: build.Node.Id}) error = %v, want nil", err)
				}
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
