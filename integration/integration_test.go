package integration

import (
	"context"
	"embed"
	"fmt"
	"log"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/sanity-io/litter"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

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
	token := MustEnv("BUILDKITE_TOKEN")
	org := MustEnv("BUILDKITE_ORG")
	agentToken := MustEnv("BUILDKITE_AGENT_TOKEN")
	graphqlClient := api.NewClient(token)

	getOrg, err := api.GetOrganization(ctx, graphqlClient, org)
	if err != nil {
		t.Fatalf("failed to fetch org: %v", err)
	}

	steps, err := fixtures.ReadFile("fixtures/helloworld.yaml")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

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
	if err != nil {
		t.Fatalf("failed to create pipeline: %v", err)
	}
	pipeline := createPipeline.PipelineCreate.Pipeline
	t.Cleanup(func() {
		_, err = api.PipelineDelete(ctx, graphqlClient, api.PipelineDeleteInput{
			Id: pipeline.Id,
		})
		if err != nil {
			t.Fatalf("failed to delete pipeline: %v", err)
		}
		t.Logf("deleted pipeline! %v", pipeline.Name)
	})

	runCtx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := scheduler.Run(runCtx, token, org, pipeline.Name, agentToken); err != nil {
			t.Logf("failed to run scheduler: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
	})

	createBuild, err := api.BuildCreate(ctx, graphqlClient, api.BuildCreateInput{
		PipelineID: pipeline.Id,
		Commit:     "HEAD",
		Branch:     branch,
	})
	if err != nil {
		t.Fatalf("failed to create build: %v", err)
	}
	build := createBuild.BuildCreate.Build
	if len(build.Jobs.Edges) != 1 {
		t.Fatalf("expected one job, got %s", litter.Sdump(build.Jobs.Edges))
	}
	node := build.Jobs.Edges[0].Node
	job, ok := node.(*api.BuildCreateBuildCreateBuildCreatePayloadBuildJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand)
	if !ok {
		t.Fatalf("expected job to be command type, got: %s", node.GetTypename())
	}
Out:
	for {
		getBuild, err := api.GetBuild(ctx, graphqlClient, build.Uuid)
		if err != nil {
			t.Fatalf("failed to get build: %v", err)
		}
		switch getBuild.Build.State {
		case api.BuildStatesPassed:
			t.Log("build passed!")
			break Out
		case api.BuildStatesFailed:
			t.Fatalf("build failed")
		default:
			t.Logf("build state: %s, sleeping", getBuild.Build.State)
			time.Sleep(time.Second)
		}
	}

	config, err := buildkite.NewTokenConfig(token, false)

	if err != nil {
		t.Fatalf("client config failed: %s", err)
	}

	client := buildkite.NewClient(config.Client())
	logs, _, err := client.Jobs.GetJobLog(org, pipeline.Name, strconv.Itoa(build.Number), job.Uuid)
	if err != nil {
		t.Fatalf("failed to fetch logs for job: %v", err)
	}
	if logs.Content == nil {
		t.Fatal("expected logs to not be nil")
	}
	if !strings.Contains(*logs.Content, "Buildkite Agent Stack for Kubernetes") {
		t.Fatalf(`failed to find README content in job logs: %v`, *logs.Content)
	}
}

func MustEnv(key string) string {
	if v, ok := syscall.Getenv(key); ok {
		return v
	}

	log.Fatalf("variable '%s' cannot be found in the environment", key)
	return ""
}
