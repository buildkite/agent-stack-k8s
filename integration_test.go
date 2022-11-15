package main

import (
	"context"
	"fmt"
	"log"
	"syscall"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

const (
	defaultSteps = `steps:
  - label: ":pipeline: Pipeline upload"
    command: ./pipeline.sh`

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
	_, err = api.PipelineDelete(ctx, graphqlClient, api.PipelineDeleteInput{
		Id: createPipeline.PipelineCreate.Pipeline.Id,
	})
	if err != nil {
		t.Fatalf("failed to delete pipeline: %v", err)
	}
	t.Logf("deleted pipeline! %v", createPipeline.PipelineCreate.Pipeline.Id)
}

func MustEnv(key string) string {
	if v, ok := syscall.Getenv(key); ok {
		return v
	}

	log.Fatalf("variable '%s' cannot be found in the environment", key)
	return ""
}
