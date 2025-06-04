package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
	"github.com/buildkite/go-buildkite/v3/buildkite"
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

// k8s labels are limited to length 63, we use the pipeline name as a label.
// So we sometimes need to limit the length of the pipeline name too.
func (t *testcase) ShortPipelineName() string {
	return strings.Trim(t.PipelineName[:min(len(t.PipelineName), 63)], "-")
}

func (t testcase) Init() testcase {
	t.Helper()
	t.Parallel()

	if t.PipelineName == "" {
		namePrefix := t.Name()
		jobID := os.Getenv("BUILDKITE_JOB_ID")
		if jobID == "" {
			jobID = strconv.FormatInt(time.Now().UnixNano(), 10)
		}
		t.PipelineName = strings.ToLower(fmt.Sprintf("test-%s-%s", namePrefix, jobID))
	}

	t.Logger = zaptest.NewLogger(t).Named(t.Name())

	clientConfig, err := restconfig.GetConfig()
	require.NoError(t, err)

	clientset, err := kubernetes.NewForConfig(clientConfig)
	require.NoError(t, err)
	t.Kubernetes = clientset

	client, err := buildkite.NewOpts(buildkite.WithTokenAuth(cfg.BuildkiteToken))
	require.NoError(t, err)
	t.Buildkite = client

	return t
}

// Create ephemeral test queues and pipelines, return pipeline's GraphQL ID.
// Register their cleanup as test cleanup.
// So when test ends, those queues and pipelines get deleted.
func (t testcase) PrepareQueueAndPipelineWithCleanup(ctx context.Context) string {
	t.Helper()

	var queueName string
	if cfg.ClusterUUID == "" {
		// TODO: This condition will be removed by subsequent PRs because we aim to eliminate non-clustered accounts.
		t.Log("No cluster-id is specified, assuming non clustered setup, skipping cluster queue creation...")
	} else {
		queue := t.createClusterQueueWithCleanup()
		queueName = *queue.Key
	}

	if queueName == "" {
		queueName = t.ShortPipelineName()
	}
	p := t.createPipelineWithCleanup(ctx, queueName)
	return *p.GraphQLID
}

func (t testcase) createClusterQueueWithCleanup() *buildkite.ClusterQueue {
	t.Helper()

	queueName := t.ShortPipelineName()
	queue, _, err := t.Buildkite.ClusterQueues.Create(cfg.Org, cfg.ClusterUUID, &buildkite.ClusterQueueCreate{
		Key: &queueName,
	})
	require.NoError(t, err)

	EnsureCleanup(t.T, func() {
		if t.preserveEphemeralObjects() {
			return
		}

		_, err := t.Buildkite.ClusterQueues.Delete(cfg.Org, cfg.ClusterUUID, *queue.ID)
		if err != nil {
			t.Errorf("Unable to clean up cluster queue %s: %v", *queue.ID, err)
			return
		}
		t.Logf("deleted cluster queue! %s", *queue.ID)
	})

	return queue
}

func (t testcase) createPipelineWithCleanup(ctx context.Context, queueName string) *buildkite.Pipeline {
	t.Helper()

	tpl, err := template.ParseFS(fixtures, fmt.Sprintf("fixtures/%s", t.Fixture))
	require.NoError(t, err)

	var steps bytes.Buffer
	require.NoError(t, tpl.Execute(&steps, map[string]string{"queue": queueName}))
	pipeline, _, err := t.Buildkite.Pipelines.Create(cfg.Org, &buildkite.CreatePipeline{
		Name:       t.PipelineName,
		Repository: t.Repo,
		ProviderSettings: &buildkite.GitHubSettings{
			TriggerMode: strPtr("none"),
		},
		Configuration: steps.String(),
		ClusterID:     cfg.ClusterUUID,
	})
	require.NoError(t, err)
	EnsureCleanup(t.T, func() {
		if !t.preserveEphemeralObjects() {
			t.deletePipeline(ctx)
		}
	})

	return pipeline
}

func (t testcase) preserveEphemeralObjects() bool {
	return preservePipelines || t.Failed()
}

func (t testcase) StartController(ctx context.Context, cfg config.Config) {
	t.Helper()

	runCtx, cancel := context.WithCancel(ctx)
	EnsureCleanup(t.T, cancel)

	// TODO: Use queue name created above
	cfg.Tags = []string{fmt.Sprintf("queue=%s", t.ShortPipelineName())}
	cfg.Debug = true

	go controller.Run(runCtx, t.Logger, t.Kubernetes, &cfg)
}

func (t testcase) TriggerBuild(ctx context.Context, pipelineID string) api.Build {
	t.Helper()

	authorEmail := os.Getenv("BUILDKITE_BUILD_CREATOR_EMAIL")
	authorName := os.Getenv("BUILDKITE_BUILD_CREATOR")
	if authorName == "" {
		authorName = "Agent Stack K8s Integration Test"
	}

	// trigger build
	createBuild, err := api.BuildCreate(ctx, t.GraphQL, api.BuildCreateInput{
		Author: api.BuildAuthorInput{
			Email: authorEmail,
			Name:  authorName,
		},
		PipelineID: pipelineID,
		Commit:     "HEAD",
		Branch:     branch,
		Message:    t.Name(),
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

	t.Logf("Triggered build: https://buildkite.com/buildkite-kubernetes-stack/%s/builds/%d", t.PipelineName, build.Number)

	return build.Build
}

func (t testcase) AssertSuccess(ctx context.Context, build api.Build) {
	t.Helper()
	require.Equal(t, api.BuildStatesPassed, t.waitForBuild(ctx, build))
}

func (t testcase) FetchLogs(build api.Build) string {
	t.Helper()

	client, err := buildkite.NewOpts(buildkite.WithTokenAuth(cfg.BuildkiteToken))
	require.NoError(t, err)
	t.Buildkite = client

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

	return logs.String()
}

func (t testcase) AssertLogsContain(build api.Build, content string) {
	t.Helper()

	assert.Contains(t, t.FetchLogs(build), content)
}

func (t testcase) AssertArtifactsContain(build api.Build, expected ...string) {
	t.Helper()
	client, err := buildkite.NewOpts(buildkite.WithTokenAuth(cfg.BuildkiteToken))
	require.NoError(t, err)
	t.Buildkite = client

	artifacts, _, err := client.Artifacts.ListByBuild(
		cfg.Org,
		t.PipelineName,
		strconv.Itoa(build.Number),
		nil,
	)
	require.NoError(t, err)

	filenames := make([]string, 0, len(artifacts))
	for _, filename := range artifacts {
		filenames = append(filenames, *filename.Filename)
	}
	for _, e := range expected {
		assert.True(t, slices.Contains(filenames, e), "expected %v to contain %v", filenames, e)
	}
}

func (t testcase) AssertFail(ctx context.Context, build api.Build) {
	t.Helper()
	assert.Equal(t, api.BuildStatesFailed, t.waitForBuild(ctx, build))
}

func (t testcase) AssertCancelled(ctx context.Context, build api.Build) {
	t.Helper()
	assert.Equal(t, api.BuildStatesCanceled, t.waitForBuild(ctx, build))
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

	tagReq, err := labels.NewRequirement("tag.buildkite.com/queue", selection.Equals, []string{t.ShortPipelineName()})
	require.NoError(t, err)

	selector := labels.NewSelector().Add(*tagReq)

	jobs, err := t.Kubernetes.BatchV1().
		Jobs(cfg.Namespace).
		List(ctx, v1.ListOptions{LabelSelector: selector.String()})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)

	for k, v := range annotations {
		assert.Equal(t, jobs.Items[0].Annotations[k], v)
		assert.Equal(t, jobs.Items[0].Spec.Template.Annotations[k], v)
	}
	for k, v := range labelz {
		assert.Equal(t, jobs.Items[0].Labels[k], v)
		assert.Equal(t, jobs.Items[0].Spec.Template.Labels[k], v)
	}
}

func (t testcase) AssertHostAlias(ctx context.Context, alias string, host string) {
	t.Helper()

	tagReq, err := labels.NewRequirement("tag.buildkite.com/queue", selection.Equals, []string{t.ShortPipelineName()})
	require.NoError(t, err)

	selector := labels.NewSelector().Add(*tagReq)

	jobs, err := t.Kubernetes.BatchV1().
		Jobs(cfg.Namespace).
		List(ctx, v1.ListOptions{LabelSelector: selector.String()})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)

	for _, hostAlias := range jobs.Items[0].Spec.Template.Spec.HostAliases {
		if hostAlias.IP == host {
			for _, actualAlias := range hostAlias.Hostnames {
				if actualAlias == alias {
					return
				}
			}
		}
	}

	assert.Fail(t, "host alias not found")
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
