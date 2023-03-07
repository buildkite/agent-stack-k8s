package integration_test

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/controller"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/buildkite/roko"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

var branch = ""

const (
	repoHTTP = "https://github.com/buildkite/agent-stack-k8s"
	repoSSH  = "git@github.com:buildkite/agent-stack-k8s"
)

var (
	preservePipelines       bool
	deleteOrphanedPipelines bool
	cfg                     api.Config

	//go:embed fixtures/*
	fixtures embed.FS
)

// hacks to make --config work
func TestMain(m *testing.M) {
	if err := os.Chdir(".."); err != nil {
		log.Fatal(err)
	}
	cmd := controller.New()
	cmd.Flags().BoolVar(&preservePipelines, "preserve-pipelines", false, "preserve pipelines created by tests")
	cmd.Flags().BoolVar(&deleteOrphanedPipelines, "delete-orphaned-pipelines", false, "delete all pipelines matching agent-k8s-*")
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
	tc.AssertMetadata(ctx, map[string]string{"some-annotation": "cool"}, map[string]string{"some-label": "wow"})
}

func TestSSHRepoClone(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "secretref.yaml",
		Repo:    repoSSH,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	_, err := tc.Kubernetes.CoreV1().Secrets(cfg.Namespace).Get(ctx, "agent-stack-k8s", v1.GetOptions{})
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
		build, _, err := tc.Buildkite.Builds.Get(cfg.Org, tc.PipelineName, fmt.Sprintf("%d", buildID), nil)
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
		build, _, err := tc.Buildkite.Builds.Get(cfg.Org, tc.PipelineName, fmt.Sprintf("%d", buildID), nil)
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
	tc.AssertLogsContain(build, `is invalid: spec.template.spec.containers[0].volumeMounts[0].name: Not found: "this-doesnt-exist"`)
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

	pipelines, err := api.SearchPipelines(ctx, graphqlClient, cfg.Org, "agent-k8s-", 100)
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(len(pipelines.Organization.Pipelines.Edges))
	for _, pipeline := range pipelines.Organization.Pipelines.Edges {
		pipeline := pipeline // prevent loop variable capture
		t.Run(pipeline.Node.Name, func(t *testing.T) {
			builds, err := api.GetBuilds(ctx, graphqlClient, fmt.Sprintf("%s/%s", cfg.Org, pipeline.Node.Name), []api.BuildStates{api.BuildStatesRunning}, 100)
			require.NoError(t, err)
			for _, build := range builds.Pipeline.Builds.Edges {
				_, err = api.BuildCancel(ctx, graphqlClient, api.BuildCancelInput{Id: build.Node.Id})
				assert.NoError(t, err)
			}
			tc := testcase{
				T:       t,
				GraphQL: api.NewClient(cfg.BuildkiteToken),
			}.Init()
			tc.PipelineName = pipeline.Node.Name
			tc.deletePipeline(context.Background())
		})
	}
}

type testcase struct {
	*testing.T
	Logger       *zap.Logger
	Fixture      string
	Repo         string
	GraphQL      graphql.Client
	Kubernetes   kubernetes.Interface
	Buildkite    *buildkite.Client
	PipelineName string
}

func (t testcase) Init() testcase {
	t.Helper()
	t.Parallel()

	t.PipelineName = fmt.Sprintf("agent-k8s-%s-%d", strings.ToLower(t.Name()), time.Now().UnixNano())
	t.Logger = zaptest.NewLogger(t).Named(t.Name())

	clientConfig, err := restconfig.GetConfig()
	require.NoError(t, err)
	clientset, err := kubernetes.NewForConfig(clientConfig)
	require.NoError(t, err)
	t.Kubernetes = clientset
	config, err := buildkite.NewTokenConfig(cfg.BuildkiteToken, false)
	require.NoError(t, err)

	t.Buildkite = buildkite.NewClient(config.Client())

	return t
}

func (t testcase) CreatePipeline(ctx context.Context) string {
	t.Helper()

	tpl, err := template.ParseFS(fixtures, fmt.Sprintf("fixtures/%s", t.Fixture))
	require.NoError(t, err)

	var steps bytes.Buffer
	require.NoError(t, tpl.Execute(&steps, map[string]string{
		"queue": t.PipelineName,
	}))
	pipeline, _, err := t.Buildkite.Pipelines.Create(cfg.Org, &buildkite.CreatePipeline{
		Name:       t.PipelineName,
		Repository: t.Repo,
		ProviderSettings: &buildkite.GitHubSettings{
			TriggerMode: strPtr("none"),
		},
		Configuration: steps.String(),
	})
	require.NoError(t, err)

	if !preservePipelines {
		t.deletePipeline(ctx)
	}

	return *pipeline.GraphQLID
}

func (t testcase) StartController(ctx context.Context, cfg api.Config) {
	t.Helper()

	runCtx, cancel := context.WithCancel(ctx)
	EnsureCleanup(t.T, cancel)

	cfg.Tags = []string{fmt.Sprintf("queue=%s", t.PipelineName)}
	cfg.Debug = true
	go controller.Run(runCtx, t.Kubernetes, cfg)
}

func (t testcase) TriggerBuild(ctx context.Context, pipelineID string) api.Build {
	t.Helper()

	// trigger build
	createBuild, err := api.BuildCreate(ctx, t.GraphQL, api.BuildCreateInput{
		PipelineID: pipelineID,
		Commit:     "HEAD",
		Branch:     branch,
	})
	require.NoError(t, err)
	EnsureCleanup(t.T, func() {
		if _, err := api.BuildCancel(ctx, t.GraphQL, api.BuildCancelInput{
			Id: createBuild.BuildCreate.Build.Id,
		}); err != nil {
			if ignorableError(err) {
				return
			}
			t.Logf("failed to cancel build: %v", err)
		}
	})
	build := createBuild.BuildCreate.Build
	require.GreaterOrEqual(t, len(build.Jobs.Edges), 1)
	node := build.Jobs.Edges[0].Node
	_, ok := node.(*api.JobJobTypeCommand)
	require.True(t, ok)

	return build.Build
}

func (t testcase) AssertSuccess(ctx context.Context, build api.Build) {
	t.Helper()
	require.Equal(t, api.BuildStatesPassed, t.waitForBuild(ctx, build))
}

func (t testcase) AssertLogsContain(build api.Build, content string) {
	t.Helper()
	config, err := buildkite.NewTokenConfig(cfg.BuildkiteToken, false)
	require.NoError(t, err)

	client := buildkite.NewClient(config.Client())
	job := build.Jobs.Edges[0].Node.(*api.JobJobTypeCommand)
	logs, _, err := client.Jobs.GetJobLog(cfg.Org, t.PipelineName, strconv.Itoa(build.Number), job.Uuid)
	require.NoError(t, err)
	require.NotNil(t, logs.Content)
	require.Contains(t, *logs.Content, content)
}

func (t testcase) AssertArtifactsContain(build api.Build, expected ...string) {
	t.Helper()
	config, err := buildkite.NewTokenConfig(cfg.BuildkiteToken, false)
	require.NoError(t, err)
	client := buildkite.NewClient(config.Client())

	artifacts, _, err := client.Artifacts.ListByBuild(cfg.Org, t.PipelineName, strconv.Itoa(build.Number), nil)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	filenames := []string{*artifacts[0].Filename, *artifacts[1].Filename}
	for _, filename := range expected {
		require.Contains(t, filenames, filename)
	}
}

func (t testcase) AssertFail(ctx context.Context, build api.Build) {
	t.Helper()

	require.Equal(t, api.BuildStatesFailed, t.waitForBuild(ctx, build))
}

func (t testcase) waitForBuild(ctx context.Context, build api.Build) api.BuildStates {
	t.Helper()

	for {
		getBuild, err := api.GetBuild(ctx, t.GraphQL, build.Uuid)
		require.NoError(t, err)
		switch getBuild.Build.State {
		case api.BuildStatesPassed, api.BuildStatesFailed, api.BuildStatesCanceled, api.BuildStatesCanceling:
			return getBuild.Build.State
		case api.BuildStatesScheduled, api.BuildStatesRunning:
			t.Logger.Debug("sleeping", zap.Any("build state", getBuild.Build.State))
			time.Sleep(time.Second)
		default:
			t.Errorf("unknown build state %q", getBuild.Build.State)
			return getBuild.Build.State
		}
	}
}

func (t testcase) AssertMetadata(ctx context.Context, annotations, labelz map[string]string) {
	t.Helper()

	tagReq, err := labels.NewRequirement(api.TagLabel, selection.Equals, []string{fmt.Sprintf("queue_%s", t.PipelineName)})
	require.NoError(t, err)
	selector := labels.NewSelector().Add(*tagReq)

	jobs, err := t.Kubernetes.BatchV1().Jobs(cfg.Namespace).List(ctx, v1.ListOptions{LabelSelector: selector.String()})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)
	for k, v := range annotations {
		require.Equal(t, jobs.Items[0].ObjectMeta.Annotations[k], v)
		require.Equal(t, jobs.Items[0].Spec.Template.Annotations[k], v)
	}
	for k, v := range labelz {
		require.Equal(t, jobs.Items[0].ObjectMeta.Labels[k], v)
		require.Equal(t, jobs.Items[0].Spec.Template.Labels[k], v)
	}
}

func strPtr(p string) *string {
	return &p
}

func ignorableError(err error) bool {
	reasons := []string{
		"already finished",
		"already being canceled",
		"already been canceled",
		"No build found",
	}
	for _, reason := range reasons {
		if strings.Contains(err.Error(), reason) {
			return true
		}
	}
	return false
}

func (t testcase) deletePipeline(ctx context.Context) {
	t.Helper()

	EnsureCleanup(t.T, func() {
		err := roko.NewRetrier(
			roko.WithMaxAttempts(10),
			roko.WithStrategy(roko.Exponential(time.Second, 5*time.Second)),
		).DoWithContext(ctx, func(r *roko.Retrier) error {
			resp, err := t.Buildkite.Pipelines.Delete(cfg.Org, t.PipelineName)
			if err != nil {
				if resp.StatusCode == http.StatusNotFound {
					return nil
				}
				t.Logf("waiting for build to be canceled on pipeline %s", t.PipelineName)
				return err
			}
			return nil
		})
		if err != nil {
			t.Logf("failed to cleanup pipeline %s: %v", t.PipelineName, err)
			return
		}
		t.Logf("deleted pipeline! %s", t.PipelineName)
	})
}
