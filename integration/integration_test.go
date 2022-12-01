package integration

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

var (
	preservePipelines *bool = flag.Bool("preserve-pipelines", false, "preserve pipelines created by tests")
	preservePods      *bool = flag.Bool("preserve-pods", false, "preserve pods created by tests")
)

//go:embed fixtures/*
var fixtures embed.FS

const (
	repo   = "https://github.com/buildkite/agent-stack-k8s"
	branch = "v2"
)

func TestWalkingSkeleton(t *testing.T) {
	/*
		create pipeline
		start controller
		trigger build
		assert build success
	*/
	ctx := context.Background()
	token := MustEnv(t, "BUILDKITE_TOKEN")
	org := MustEnv(t, "BUILDKITE_ORG")
	agentToken := MustEnv(t, "BUILDKITE_AGENT_TOKEN")
	graphqlClient := api.NewClient(token)

	getOrg, err := api.GetOrganization(ctx, graphqlClient, org)
	assert.NoError(t, err)

	steps, err := fixtures.ReadFile("fixtures/helloworld.yaml")
	assert.NoError(t, err)

	createPipeline, err := api.PipelineCreate(ctx, graphqlClient, api.PipelineCreateInput{
		OrganizationId: getOrg.Organization.Id,
		Name:           fmt.Sprintf("agent-k8s-%d", time.Now().UnixNano()),
		Repository: api.PipelineRepositoryInput{
			Url: repo,
		},
		Steps: api.PipelineStepsInput{
			Yaml: string(steps),
		},
	})
	assert.NoError(t, err)

	pipeline := createPipeline.PipelineCreate.Pipeline
	if !*preservePipelines {
		t.Cleanup(func() {
			_, err = api.PipelineDelete(ctx, graphqlClient, api.PipelineDeleteInput{
				Id: pipeline.Id,
			})
			assert.NoError(t, err)
			t.Logf("deleted pipeline! %v", pipeline.Name)
		})
	}
	logger, err := zap.NewDevelopment()
	assert.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	go func() {
		assert.NoError(t, scheduler.Run(runCtx, logger.Named("scheduler"), monitor.New(logger.Named("monitor"), token), scheduler.Config{
			Org:         org,
			Pipeline:    pipeline.Name,
			AgentToken:  agentToken,
			DeletePods:  !*preservePods,
			MaxInFlight: 1,
		}))
	}()
	t.Cleanup(func() {
		cancel()
	})

	createBuild, err := api.BuildCreate(ctx, graphqlClient, api.BuildCreateInput{
		PipelineID: pipeline.Id,
		Commit:     "HEAD",
		Branch:     branch,
	})
	assert.NoError(t, err)
	build := createBuild.BuildCreate.Build
	assert.Len(t, build.Jobs.Edges, 1)
	node := build.Jobs.Edges[0].Node
	job, ok := node.(*api.BuildCreateBuildCreateBuildCreatePayloadBuildJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand)
	assert.True(t, ok)
Out:
	for {
		getBuild, err := api.GetBuild(ctx, graphqlClient, build.Uuid)
		assert.NoError(t, err)
		switch getBuild.Build.State {
		case api.BuildStatesPassed:
			logger.Debug("build passed!")
			break Out
		case api.BuildStatesFailed:
			t.Fatalf("build failed")
		default:
			logger.Debug("sleeping", zap.Any("build state", getBuild.Build.State))
			time.Sleep(time.Second)
		}
	}

	config, err := buildkite.NewTokenConfig(token, false)
	assert.NoError(t, err)

	client := buildkite.NewClient(config.Client())
	logs, _, err := client.Jobs.GetJobLog(org, pipeline.Name, strconv.Itoa(build.Number), job.Uuid)
	assert.NoError(t, err)
	assert.NotNil(t, logs.Content)
	assert.Contains(t, *logs.Content, "Buildkite Agent Stack for Kubernetes")

	artifacts, _, err := client.Artifacts.ListByBuild(org, pipeline.Name, strconv.Itoa(build.Number), nil)
	assert.NoError(t, err)
	assert.Len(t, artifacts, 2)
	filenames := []string{*artifacts[0].Filename, *artifacts[1].Filename}
	assert.Contains(t, filenames, "README.md")
	assert.Contains(t, filenames, "CODE_OF_CONDUCT.md")
}

func MustEnv(t *testing.T, key string) string {
	if v, ok := syscall.Getenv(key); ok {
		return v
	}

	t.Fatalf("variable '%s' cannot be found in the environment", key)
	return ""
}
