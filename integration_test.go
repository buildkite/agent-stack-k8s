package main

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/pkg/scheduler"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

const (
	defaultSteps = `steps:
  - label: ":wave:"
    command: "echo hello world"`

	repo = "https://github.com/buildkite/agent-stack-k8s"
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

	createPipeline, err := api.PipelineCreate(ctx, graphqlClient, api.PipelineCreateInput{
		OrganizationId: getOrg.Organization.Id,
		Name:           fmt.Sprintf("agent-k8s-%d", time.Now().UnixNano()),
		Repository: api.PipelineRepositoryInput{
			Url: repo,
		},
		Steps: api.PipelineStepsInput{
			Yaml: defaultSteps,
		},
	})
	if err != nil {
		t.Fatalf("failed to create pipeline: %v", err)
	}
	t.Cleanup(func() {
		_, err = api.PipelineDelete(ctx, graphqlClient, api.PipelineDeleteInput{
			Id: createPipeline.PipelineCreate.Pipeline.Id,
		})
		if err != nil {
			t.Fatalf("failed to delete pipeline: %v", err)
		}
		t.Logf("deleted pipeline! %v", createPipeline.PipelineCreate.Pipeline.Name)
	})

	runCtx, cancel := context.WithCancel(context.Background())
	go scheduler.Run(runCtx, token, org, createPipeline.PipelineCreate.Pipeline.Name, agentToken)
	t.Cleanup(func() {
		cancel()
	})

	createBuild, err := api.BuildCreate(ctx, graphqlClient, api.BuildCreateInput{
		PipelineID: createPipeline.PipelineCreate.Pipeline.Id,
		Commit:     "HEAD",
		Branch:     "main",
	})
	if err != nil {
		t.Fatalf("failed to create build: %v", err)
	}
Out:
	for {
		getBuild, err := api.GetBuild(ctx, graphqlClient, createBuild.BuildCreate.Build.Uuid)
		if err != nil {
			t.Fatalf("failed to get build: %v", err)
		}
		switch getBuild.Build.State {
		case api.BuildStatesPassed:
			break Out
		case api.BuildStatesFailed:
			t.Fatalf("build failed")
		default:
			t.Logf("build state: %s, sleeping", getBuild.Build.State)
			time.Sleep(time.Second)
		}
	}
}
