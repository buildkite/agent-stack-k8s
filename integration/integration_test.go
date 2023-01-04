package integration

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"strconv"
	"syscall"
	"testing"
	"text/template"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	preservePipelines *bool = flag.Bool("preserve-pipelines", false, "preserve pipelines created by tests")
)

//go:embed fixtures/*
var fixtures embed.FS

const (
	repoHTTP = "https://github.com/buildkite/agent-stack-k8s"
	repoSSH  = "git@github.com:buildkite/agent-stack-k8s"
	branch   = "v2"
)

func TestWalkingSkeleton(t *testing.T) {
	basicTest(t, "helloworld.yaml", repoHTTP)
}

func TestSSHRepoClone(t *testing.T) {
	k8s := newk8sClient(t)
	_, err := k8s.CoreV1().Secrets("default").Get(context.Background(), "agent-stack-k8s", v1.GetOptions{})
	require.NoError(t, err, "agent-stack-k8s secret must exist")
	basicTest(t, "secretref.yaml", repoSSH)
}

func MustEnv(t *testing.T, key string) string {
	if v, ok := syscall.Getenv(key); ok {
		return v
	}

	t.Fatalf("variable '%s' cannot be found in the environment", key)
	return ""
}

func basicTest(t *testing.T, fixture, repo string) {
	// create pipeline
	ctx := context.Background()
	token := MustEnv(t, "BUILDKITE_TOKEN")
	org := MustEnv(t, "BUILDKITE_ORG")
	agentTokenSecret := MustEnv(t, "BUILDKITE_AGENT_TOKEN_SECRET")
	graphqlClient := api.NewClient(token)

	getOrg, err := api.GetOrganization(ctx, graphqlClient, org)
	require.NoError(t, err)

	tpl, err := template.ParseFS(fixtures, fmt.Sprintf("fixtures/%s", fixture))
	require.NoError(t, err)

	pipelineName := fmt.Sprintf("agent-k8s-%d", time.Now().UnixNano())
	var steps bytes.Buffer
	require.NoError(t, tpl.Execute(&steps, map[string]string{
		"queue": pipelineName,
	}))
	createPipeline, err := api.PipelineCreate(ctx, graphqlClient, api.PipelineCreateInput{
		OrganizationId: getOrg.Organization.Id,
		Name:           pipelineName,
		Repository: api.PipelineRepositoryInput{
			Url: repo,
		},
		Steps: api.PipelineStepsInput{
			Yaml: steps.String(),
		},
	})
	require.NoError(t, err)

	pipeline := createPipeline.PipelineCreate.Pipeline
	if !*preservePipelines {
		EnsureCleanup(t, func() {
			_, err = api.PipelineDelete(ctx, graphqlClient, api.PipelineDeleteInput{
				Id: pipeline.Id,
			})
			assert.NoError(t, err)
			t.Logf("deleted pipeline! %v", pipeline.Name)
		})
	}

	//start controller
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, nil)
	clientConfig, err := kubeConfig.ClientConfig()
	require.NoError(t, err)

	k8sClient, err := kubernetes.NewForConfig(clientConfig)
	require.NoError(t, err)
	monitor, err := monitor.New(ctx, logger.Named("monitor"), k8sClient, monitor.Config{
		Token:       token,
		MaxInFlight: 1,
		Org:         org,
		Namespace:   api.DefaultNamespace,
		Tags:        []string{fmt.Sprintf("queue=%s", pipelineName)},
	})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	go scheduler.Run(runCtx, logger.Named("scheduler"), monitor, k8sClient, scheduler.Config{
		AgentTokenSecret: agentTokenSecret,
		JobTTL:           time.Minute,
	}.WithDefaults())
	EnsureCleanup(t, cancel)

	// trigger build
	createBuild, err := api.BuildCreate(ctx, graphqlClient, api.BuildCreateInput{
		PipelineID: pipeline.Id,
		Commit:     "HEAD",
		Branch:     branch,
	})
	require.NoError(t, err)
	EnsureCleanup(t, func() {
		api.BuildCancel(ctx, graphqlClient, api.BuildCancelInput{
			Id: createBuild.BuildCreate.Build.Id,
		})
	})
	build := createBuild.BuildCreate.Build
	require.Len(t, build.Jobs.Edges, 1)
	node := build.Jobs.Edges[0].Node
	job, ok := node.(*api.JobJobTypeCommand)
	require.True(t, ok)

	// assert build success
Out:
	for {
		getBuild, err := api.GetBuild(ctx, graphqlClient, build.Uuid)
		require.NoError(t, err)
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
	require.NoError(t, err)

	client := buildkite.NewClient(config.Client())
	logs, _, err := client.Jobs.GetJobLog(org, pipeline.Name, strconv.Itoa(build.Number), job.Uuid)
	require.NoError(t, err)
	require.NotNil(t, logs.Content)
	require.Contains(t, *logs.Content, "Buildkite Agent Stack for Kubernetes")

	artifacts, _, err := client.Artifacts.ListByBuild(org, pipeline.Name, strconv.Itoa(build.Number), nil)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	filenames := []string{*artifacts[0].Filename, *artifacts[1].Filename}
	require.Contains(t, filenames, "README.md")
	require.Contains(t, filenames, "CODE_OF_CONDUCT.md")
}

func newk8sClient(t *testing.T) kubernetes.Interface {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, nil)
	clientConfig, err := kubeConfig.ClientConfig()
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(clientConfig)
	require.NoError(t, err)

	return clientset
}

func TestCleanupOrphanedPipelines(t *testing.T) {
	if *preservePipelines {
		t.Skip("not cleaning orphaned pipelines")
	}
	ctx := context.Background()
	token := MustEnv(t, "BUILDKITE_TOKEN")
	org := MustEnv(t, "BUILDKITE_ORG")
	graphqlClient := api.NewClient(token)

	pipelines, err := api.SearchPipelines(ctx, graphqlClient, org, "agent-k8s-", 100)
	require.NoError(t, err)
	for _, pipeline := range pipelines.Organization.Pipelines.Edges {
		builds, err := api.GetBuilds(ctx, graphqlClient, fmt.Sprintf("%s/%s", org, pipeline.Node.Name), []api.BuildStates{api.BuildStatesRunning}, 100)
		require.NoError(t, err)
		for _, build := range builds.Pipeline.Builds.Edges {
			_, err = api.BuildCancel(ctx, graphqlClient, api.BuildCancelInput{Id: build.Node.Id})
			require.NoError(t, err)
		}
		_, err = api.PipelineDelete(ctx, graphqlClient, api.PipelineDeleteInput{
			Id: pipeline.Node.Id,
		})
		require.NoError(t, err)
		t.Logf("deleted orphaned pipeline! %v", pipeline.Node.Name)
	}
}
