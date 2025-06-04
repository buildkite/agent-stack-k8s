package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/Khan/genqlient/graphql"
	agentApi "github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
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
	Org          string
	ClusterUUID  string
}

// k8s labels are limited to length 63, we use the pipeline name as a label.
// So we sometimes need to limit the length of the pipeline name too.
// These pipeline name consists of {name}-{job id}
// When {name} is too longer, the {job id} bit gets too short, simple truncate 64 will make this queue name non-unique.
//
// So we do truncate(name, 63 - 8) + SHA(full name)[:8]
func (t *testcase) QueueName() string {
	if len(t.PipelineName) <= 63 {
		return t.PipelineName
	}

	// Use SHA256 hash of the pipeline name to ensure uniqueness while keeping length under 63
	hash := sha256.Sum256([]byte(t.PipelineName))
	hashPartLength := 8 // Use first 8 characters of hash
	hashStr := fmt.Sprintf("%x", hash)[:hashPartLength]

	// Keep as much of the original name as possible, then append hash
	maxOriginalLen := 63 - 1 - hashPartLength // -1 for the dash separator
	truncated := strings.Trim(t.PipelineName[:maxOriginalLen], "-")
	return fmt.Sprintf("%s-%s", truncated, hashStr)
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

	agentTokenIdentity := t.getAgentTokenIdentity()

	t.Org = agentTokenIdentity.OrganizationSlug
	t.ClusterUUID = agentTokenIdentity.ClusterUUID

	if t.ClusterUUID == "" {
		t.Fatal("Detected unclustered agent token, please upgrade to a cluster agent token: https://buildkite.com/organizations/~/clusters/~/tokens")
	}

	return t
}

// Create ephemeral test queues and pipelines, return pipeline's GraphQL ID.
// Register their cleanup as test cleanup.
// So when test ends, those queues and pipelines get deleted.
func (t testcase) PrepareQueueAndPipelineWithCleanup(ctx context.Context) string {
	t.Helper()

	var queueName string
	queue := t.createClusterQueueWithCleanup()
	queueName = *queue.Key

	if queueName == "" {
		queueName = t.QueueName()
	}
	p := t.createPipelineWithCleanup(ctx, queueName)
	return *p.GraphQLID
}

func (t testcase) createClusterQueueWithCleanup() *buildkite.ClusterQueue {
	t.Helper()

	queueName := t.QueueName()
	queue, _, err := t.Buildkite.ClusterQueues.Create(t.Org, t.ClusterUUID, &buildkite.ClusterQueueCreate{
		Key: &queueName,
	})
	if err != nil {
		t.Errorf("Unable to create cluster queue %s: %v", queueName, err)
		require.NoError(t, err)
	}

	EnsureCleanup(t.T, func() {
		if t.preserveEphemeralObjects() {
			return
		}

		if err := roko.NewRetrier(
			roko.WithMaxAttempts(5),
			roko.WithStrategy(roko.Constant(5*time.Second)),
		).DoWithContext(context.Background(), func(r *roko.Retrier) error {
			// There is a small chance that we are deleting queue too soon before queue realize agent has disconnected.
			_, err := t.Buildkite.ClusterQueues.Delete(t.Org, t.ClusterUUID, *queue.ID)
			return err
		}); err != nil {
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
	pipeline, _, err := t.Buildkite.Pipelines.Create(t.Org, &buildkite.CreatePipeline{
		Name:       t.PipelineName,
		Repository: t.Repo,
		ProviderSettings: &buildkite.GitHubSettings{
			TriggerMode: strPtr("none"),
		},
		Configuration: steps.String(),
		ClusterID:     t.ClusterUUID,
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
	cfg.Tags = []string{fmt.Sprintf("queue=%s", t.QueueName())}
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
			t.Org,
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
		t.Org,
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

	tagReq, err := labels.NewRequirement("tag.buildkite.com/queue", selection.Equals, []string{t.QueueName()})
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

	tagReq, err := labels.NewRequirement("tag.buildkite.com/queue", selection.Equals, []string{t.QueueName()})
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

func (t testcase) getAgentTokenIdentity() *agentApi.AgentTokenIdentity {
	t.Helper()
	ctx := context.Background()

	token, err := fetchAgentToken(ctx, t.Logger, t.Kubernetes, cfg.Namespace, cfg.AgentTokenSecret)
	require.NoError(t, err)

	agentEndpoint := ""
	if cfg.AgentConfig != nil && cfg.AgentConfig.Endpoint != nil {
		agentEndpoint = *cfg.AgentConfig.Endpoint
	}
	client, err := agentApi.NewAgentTokenClient(token, agentEndpoint)
	require.NoError(t, err)

	result, _, err := client.GetTokenIdentity(context.Background())
	require.NoError(t, err)

	return result
}

const agentTokenKey = "BUILDKITE_AGENT_TOKEN"

func fetchAgentToken(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface, namespace, agentTokenSecretName string) (string, error) {
	// Need to fetch the agent token ourselves.
	tokenSecret, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, agentTokenSecretName, v1.GetOptions{})
	if err != nil {
		logger.Error("fetching agent token from secret", zap.Error(err))
		return "", err
	}
	agentToken := string(tokenSecret.Data[agentTokenKey])
	if agentToken == "" {
		logger.Error("agent token is empty")
		return "", errors.New("agent token is empty")
	}
	return agentToken, nil
}
