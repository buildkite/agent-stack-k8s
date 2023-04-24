package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
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

	queuePrefix := "queue_"
	namePrefix := "agent-stack-k8s-test-"
	nameVariable := fmt.Sprintf("%s-%d", strings.ToLower(t.Name()), time.Now().UnixNano())
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(nameVariable)))

	// labels are limited to length 63
	t.PipelineName = fmt.Sprintf("%s%s", namePrefix, hash[:63-len(namePrefix)-len(queuePrefix)])
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

func (t testcase) StartController(ctx context.Context, cfg config.Config) {
	t.Helper()

	runCtx, cancel := context.WithCancel(ctx)
	EnsureCleanup(t.T, cancel)

	cfg.Tags = []string{fmt.Sprintf("queue=%s", t.PipelineName)}
	cfg.Debug = true

	go controller.Run(runCtx, t.Logger, t.Kubernetes, cfg)
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

	var logs strings.Builder
	for _, edge := range build.Jobs.Edges {
		job, wasJob := edge.Node.(*api.JobJobTypeCommand)
		if !assert.True(t, wasJob) {
			continue
		}

		jobLog, _, err := client.Jobs.GetJobLog(
			cfg.Org,
			t.PipelineName,
			strconv.Itoa(build.Number),
			job.Uuid,
		)
		if !assert.NoError(t, err) || !assert.NotNil(t, jobLog.Content) {
			continue
		}

		_, err = logs.WriteString(*jobLog.Content)
		assert.NoError(t, err)
	}

	assert.Contains(t, logs.String(), content)
}

func (t testcase) AssertArtifactsContain(build api.Build, expected ...string) {
	t.Helper()
	config, err := buildkite.NewTokenConfig(cfg.BuildkiteToken, false)
	require.NoError(t, err)
	client := buildkite.NewClient(config.Client())

	artifacts, _, err := client.Artifacts.ListByBuild(
		cfg.Org,
		t.PipelineName,
		strconv.Itoa(build.Number),
		nil,
	)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	filenames := []string{*artifacts[0].Filename, *artifacts[1].Filename}
	for _, filename := range expected {
		assert.Contains(t, filenames, filename)
	}
}

func (t testcase) AssertFail(ctx context.Context, build api.Build) {
	t.Helper()

	assert.Equal(t, api.BuildStatesFailed, t.waitForBuild(ctx, build))
}

func (t testcase) waitForBuild(ctx context.Context, build api.Build) api.BuildStates {
	t.Helper()

	for {
		getBuild, err := api.GetBuild(ctx, t.GraphQL, build.Uuid)
		require.NoError(t, err)
		switch getBuild.Build.State {
		case api.BuildStatesPassed,
			api.BuildStatesFailed,
			api.BuildStatesCanceled,
			api.BuildStatesCanceling:

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

	tagReq, err := labels.NewRequirement("buildkite.com/queue", selection.Equals, []string{t.PipelineName})
	require.NoError(t, err)

	selector := labels.NewSelector().Add(*tagReq)

	jobs, err := t.Kubernetes.BatchV1().
		Jobs(cfg.Namespace).
		List(ctx, v1.ListOptions{LabelSelector: selector.String()})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)

	for k, v := range annotations {
		assert.Equal(t, jobs.Items[0].ObjectMeta.Annotations[k], v)
		assert.Equal(t, jobs.Items[0].Spec.Template.Annotations[k], v)
	}
	for k, v := range labelz {
		assert.Equal(t, jobs.Items[0].ObjectMeta.Labels[k], v)
		assert.Equal(t, jobs.Items[0].Spec.Template.Labels[k], v)
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
