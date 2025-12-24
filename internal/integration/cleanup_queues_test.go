package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
	"github.com/stretchr/testify/require"
)

func TestCleanupOrphanedQueues(t *testing.T) {
	if !cleanupQueues {
		t.Skip("not cleaning orphaned queues")
	}

	ctx := context.Background()
	graphqlClient := api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint)
	tc := testcase{T: t}.Init()
	orgSlug := tc.Org

	allQueues, orgID, err := searchAllQueues(ctx, t, graphqlClient, orgSlug, "test-")
	require.NoError(t, err)

	t.Logf("found %d total queues to delete", len(allQueues))

	for _, queue := range allQueues {
		t.Run(queue.Key, func(t *testing.T) {
			_, err := api.ClusterQueueDelete(ctx, graphqlClient, api.ClusterQueueDeleteInput{
				Id:             queue.ID,
				OrganizationId: orgID,
			})
			if err != nil {
				t.Errorf("failed to delete queue %s: %v", queue.Key, err)
				return
			}
			t.Logf("deleted queue %s", queue.Key)
		})
	}
}

// queueInfo holds the essential info about a queue for cleanup
type queueInfo struct {
	ID  string
	Key string
}

// searchAllQueues fetches all queues matching the key prefix across all clusters, paginating through all results.
func searchAllQueues(
	ctx context.Context,
	t *testing.T,
	client graphql.Client,
	orgSlug string,
	keyPrefix string,
) ([]queueInfo, string, error) {
	var allQueues []queueInfo
	var clusterCursor string
	var orgID string
	pageNum := 0

	for {
		pageNum++
		resp, err := api.GetOrganizationQueues(ctx, client, orgSlug, 100, clusterCursor, 100)
		if err != nil {
			return nil, "", err
		}

		orgID = resp.Organization.Id

		for _, clusterEdge := range resp.Organization.Clusters.Edges {
			cluster := clusterEdge.Node
			queues := cluster.Queues

			// Process queues from the initial response
			for _, queueEdge := range queues.Edges {
				queue := queueEdge.Node
				if strings.HasPrefix(queue.Key, keyPrefix) {
					allQueues = append(allQueues, queueInfo{
						ID:  queue.Id,
						Key: queue.Key,
					})
				}
			}

			// Check if this cluster has more queues to paginate
			if queues.PageInfo.HasNextPage {
				// Need to paginate through this cluster's queues
				moreQueues, err := fetchAllClusterQueues(ctx, client, orgSlug, cluster.Uuid, keyPrefix, queues.PageInfo.EndCursor)
				if err != nil {
					return nil, "", err
				}
				allQueues = append(allQueues, moreQueues...)
			}
		}

		t.Logf("page %d: processed clusters, found %d matching queues so far", pageNum, len(allQueues))

		pageInfo := resp.Organization.Clusters.PageInfo
		if !pageInfo.HasNextPage {
			break
		}
		clusterCursor = pageInfo.EndCursor
	}

	return allQueues, orgID, nil
}

// fetchAllClusterQueues fetches remaining queues for a cluster starting from a cursor
func fetchAllClusterQueues(
	ctx context.Context,
	client graphql.Client,
	orgSlug string,
	clusterUUID string,
	keyPrefix string,
	startCursor string,
) ([]queueInfo, error) {
	var queues []queueInfo
	cursor := startCursor

	for {
		resp, err := api.GetClusterQueues(ctx, client, orgSlug, clusterUUID, 100, cursor)
		if err != nil {
			return nil, err
		}

		for _, edge := range resp.Organization.Cluster.Queues.Edges {
			queue := edge.Node
			if strings.HasPrefix(queue.Key, keyPrefix) {
				queues = append(queues, queueInfo{
					ID:  queue.Id,
					Key: queue.Key,
				})
			}
		}

		pageInfo := resp.Organization.Cluster.Queues.PageInfo
		if !pageInfo.HasNextPage {
			break
		}
		cursor = pageInfo.EndCursor
	}

	return queues, nil
}
