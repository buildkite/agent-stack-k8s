package integration_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWalkingSkeleton(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "helloworld.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Buildkite Agent Stack for Kubernetes")
	tc.AssertArtifactsContain(build, "README.md", "CODE_OF_CONDUCT.md")
	tc.AssertMetadata(
		ctx,
		map[string]string{"some-annotation": "cool"},
		map[string]string{"some-label": "wow"},
	)
}

func TestSSHRepoClone(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "secretref.yaml",
		Repo:    repoSSH,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	_, err := tc.Kubernetes.CoreV1().
		Secrets(cfg.Namespace).
		Get(ctx, "agent-stack-k8s", v1.GetOptions{})
	require.NoError(t, err, "agent-stack-k8s secret must exist")

	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}

func TestPluginCloneFailsTests(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "unknown-plugin.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()

	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
}

func TestMaxInFlightLimited(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "parallel.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()

	pipelineID := tc.CreatePipeline(ctx)
	cfg := cfg
	cfg.MaxInFlight = 1
	tc.StartController(ctx, cfg)
	buildID := tc.TriggerBuild(ctx, pipelineID).Number

	for {
		build, _, err := tc.Buildkite.Builds.Get(
			cfg.Org,
			tc.PipelineName,
			fmt.Sprintf("%d", buildID),
			nil,
		)
		require.NoError(t, err)
		if *build.State == "running" {
			require.LessOrEqual(t, *build.Pipeline.RunningJobsCount, cfg.MaxInFlight)
		} else if *build.State == "passed" {
			break
		} else if *build.State == "scheduled" {
			t.Log("waiting for build to start")
			time.Sleep(time.Second)
			continue
		} else {
			t.Fatalf("unexpected build state: %v", *build.State)
		}
	}
}

func TestMaxInFlightUnlimited(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "parallel.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()

	pipelineID := tc.CreatePipeline(ctx)
	cfg := cfg
	cfg.MaxInFlight = 0
	tc.StartController(ctx, cfg)
	buildID := tc.TriggerBuild(ctx, pipelineID).Number

	var maxRunningJobs int
	for {
		build, _, err := tc.Buildkite.Builds.Get(
			cfg.Org,
			tc.PipelineName,
			fmt.Sprintf("%d", buildID),
			nil,
		)
		require.NoError(t, err)
		if *build.State == "running" {
			var runningJobs int
			for _, job := range build.Jobs {
				if *job.State == "running" {
					runningJobs++
				}
			}
			t.Logf("running, runningJobs: %d", runningJobs)
			maxRunningJobs = maxOf(maxRunningJobs, runningJobs)
		} else if *build.State == "passed" {
			require.Equal(t, 4, maxRunningJobs) // all jobs should have run at once
			break
		} else if *build.State == "scheduled" {
			t.Log("waiting for build to start")
		} else {
			t.Fatalf("unexpected build state: %v", *build.State)
		}
	}
}

func TestSidecars(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "sidecars.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Welcome to nginx!")
}

func TestInvalidPodSpec(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "invalid.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(
		build,
		`is invalid: spec.template.spec.containers[0].volumeMounts[0].name: Not found: "this-doesnt-exist"`,
	)
}

func TestInvalidPodJSON(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "invalid2.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(
		build,
		"failed parsing Kubernetes plugin: json: cannot unmarshal number into Go struct field EnvVar.PodSpec.containers.env.value of type string",
	)
}

func maxOf(x, y int) int {
	if x < y {
		return y
	}
	return x
}

func TestCleanupOrphanedPipelines(t *testing.T) {
	if !deleteOrphanedPipelines {
		t.Skip("not cleaning orphaned pipelines")
	}

	ctx := context.Background()
	graphqlClient := api.NewClient(cfg.BuildkiteToken)

	pipelines, err := api.SearchPipelines(ctx, graphqlClient, cfg.Org, "agent-stack-k8s-", 100)
	require.NoError(t, err)

	numPipelines := len(pipelines.Organization.Pipelines.Edges)
	t.Logf("found %d pipelines to delete", numPipelines)

	var wg sync.WaitGroup
	wg.Add(numPipelines)
	for _, pipeline := range pipelines.Organization.Pipelines.Edges {
		pipeline := pipeline // prevent loop variable capture
		t.Run(pipeline.Node.Name, func(t *testing.T) {
			builds, err := api.GetBuilds(
				ctx,
				graphqlClient,
				fmt.Sprintf("%s/%s", cfg.Org, pipeline.Node.Name),
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
			tc := testcase{
				T:       t,
				GraphQL: api.NewClient(cfg.BuildkiteToken),
			}.Init()
			tc.PipelineName = pipeline.Node.Name
			tc.deletePipeline(ctx)
		})
	}
}

func TestEnvVariables(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "env.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Testing some env variables: set")
}

func TestImagePullBackOffCancelled(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "image-pull-back-off-cancelled.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.CreatePipeline(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(build, "other job has run")
}
