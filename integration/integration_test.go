package integration

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/cmd/controller"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	flag "github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	repoHTTP = "https://github.com/buildkite/agent-stack-k8s"
	repoSSH  = "git@github.com:buildkite/agent-stack-k8s"
	branch   = "v2"
)

var (
	preservePipelines *bool = flag.Bool("preserve-pipelines", false, "preserve pipelines created by tests")
	cfg               api.Config

	//go:embed fixtures/*
	fixtures embed.FS
)

// hacks to make --config work
func TestMain(m *testing.M) {
	if err := os.Chdir(".."); err != nil {
		log.Fatal(err)
	}
	cmd := controller.New()
	var err error
	cfg, err = controller.ParseConfig(cmd, os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chdir("integration"); err != nil {
		log.Fatal(err)
	}
	for i, v := range os.Args {
		if strings.Contains(v, "test") {
			os.Args[i] = v
		} else {
			os.Args[i] = ""
		}
	}
	os.Exit(m.Run())
}

func TestWalkingSkeleton(t *testing.T) {
	basicTest(t, "helloworld.yaml", repoHTTP)
}

func TestSSHRepoClone(t *testing.T) {
	k8s := newk8sClient(t)
	_, err := k8s.CoreV1().Secrets(cfg.Namespace).Get(context.Background(), "agent-stack-k8s", v1.GetOptions{})
	require.NoError(t, err, "agent-stack-k8s secret must exist")
	basicTest(t, "secretref.yaml", repoSSH)
}

func basicTest(t *testing.T, fixture, repo string) {
	t.Helper()
	// create pipeline
	ctx := context.Background()
	graphqlClient := api.NewClient(cfg.BuildkiteToken)

	getOrg, err := api.GetOrganization(ctx, graphqlClient, cfg.Org)
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

	runCtx, cancel := context.WithCancel(context.Background())
	EnsureCleanup(t, cancel)

	cfg.Tags = []string{fmt.Sprintf("queue=%s", pipelineName)}
	go controller.Run(runCtx, k8sClient, cfg)

	// trigger build
	createBuild, err := api.BuildCreate(ctx, graphqlClient, api.BuildCreateInput{
		PipelineID: pipeline.Id,
		Commit:     "HEAD",
		Branch:     branch,
	})
	require.NoError(t, err)
	EnsureCleanup(t, func() {
		if _, err := api.BuildCancel(ctx, graphqlClient, api.BuildCancelInput{
			Id: createBuild.BuildCreate.Build.Id,
		}); err != nil {
			t.Logf("failed to cancel build: %v", err)
		}
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

	config, err := buildkite.NewTokenConfig(cfg.BuildkiteToken, false)
	require.NoError(t, err)

	client := buildkite.NewClient(config.Client())
	logs, _, err := client.Jobs.GetJobLog(cfg.Org, pipeline.Name, strconv.Itoa(build.Number), job.Uuid)
	require.NoError(t, err)
	require.NotNil(t, logs.Content)
	require.Contains(t, *logs.Content, "Buildkite Agent Stack for Kubernetes")

	artifacts, _, err := client.Artifacts.ListByBuild(cfg.Org, pipeline.Name, strconv.Itoa(build.Number), nil)
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
	graphqlClient := api.NewClient(cfg.BuildkiteToken)

	pipelines, err := api.SearchPipelines(ctx, graphqlClient, cfg.Org, "agent-k8s-", 100)
	require.NoError(t, err)
	for _, pipeline := range pipelines.Organization.Pipelines.Edges {
		builds, err := api.GetBuilds(ctx, graphqlClient, fmt.Sprintf("%s/%s", cfg.Org, pipeline.Node.Name), []api.BuildStates{api.BuildStatesRunning}, 100)
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
