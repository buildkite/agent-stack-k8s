package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWalkingSkeleton(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "helloworld.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Buildkite Agent Stack for Kubernetes")
	tc.AssertArtifactsContain(build, "README.md", "CODE_OF_CONDUCT.md")
	tc.AssertMetadata(
		ctx,
		map[string]string{"some-annotation": "cool"},
		map[string]string{"some-label": "wow"},
	)
}

func TestPodSpecPatchInStep(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "podspecpatch-step.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "value of MOUNTAIN is cotopaxi")
}

func TestPodSpecPatchInStepFailsWhenPatchingContainerCommands(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "podspecpatch-command-step.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(build, fmt.Sprintf("%v", scheduler.ErrNoCommandModification))
}

func TestPodSpecPatchInController(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "mountain.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.PodSpecPatch = &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name: "mountain",
				Env: []corev1.EnvVar{
					{
						Name:  "MOUNTAIN",
						Value: "antisana",
					},
				},
			},
		},
	}

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "value of MOUNTAIN is antisana")
}

func TestControllerPicksUpJobsWithSubsetOfAgentTags(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "helloworld.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	cfg := cfg
	cfg.Tags = append(cfg.Tags, "foo=bar") // job has queue=<something>, agent has queue=<something> and foo=bar

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}

func TestControllerSetsAdditionalRedactedVars(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "redacted-vars.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	cfg := cfg
	cfg.AdditionalRedactedVars = []string{"ELEVEN_HERBS_AND_SPICES"}

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "Redaction should work in the checkout container")
	assert.Contains(t, logs, "Redaction should work in the command container:")
	assert.NotContains(t, logs, "white pepper and 10 others")
}

func TestPrePostCheckoutHooksRun(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "plugin-checkout-hook.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "The pre-checkout hook ran!")
	assert.Contains(t, logs, "The post-checkout hook ran!")
}

func TestChown(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "chown.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertArtifactsContain(build, "some-file")
}

func TestSSHRepoClone(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "secretref.yaml",
		Repo:    repoSSH,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()

	ctx := context.Background()
	_, err := tc.Kubernetes.CoreV1().
		Secrets(cfg.Namespace).
		Get(ctx, "integration-test-ssh-key", metav1.GetOptions{})
	require.NoError(t, err, "agent-stack-k8s secret must exist")

	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
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

	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
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

	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.MaxInFlight = 1
	tc.StartController(ctx, cfg)
	buildID := tc.TriggerBuild(ctx, pipelineID).Number

	for {
		build, _, err := tc.Buildkite.Builds.Get(
			cfg.Org,
			tc.PipelineName,
			fmt.Sprintf("%d", buildID),
			nil,
		)
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

	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.MaxInFlight = 0
	tc.StartController(ctx, cfg)
	buildID := tc.TriggerBuild(ctx, pipelineID).Number

	var maxRunningJobs int
	for {
		build, _, err := tc.Buildkite.Builds.Get(
			cfg.Org,
			tc.PipelineName,
			fmt.Sprintf("%d", buildID),
			nil,
		)
		require.NoError(t, err)
		if *build.State == "running" {
			var runningJobs int
			for _, job := range build.Jobs {
				if *job.State == "running" {
					runningJobs++
				}
			}
			t.Logf("running, runningJobs: %d", runningJobs)
			maxRunningJobs = max(maxRunningJobs, runningJobs)
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
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Welcome to nginx!")
}

func TestExtraVolumeMounts(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "extra-volume-mounts.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}

func TestInvalidPodSpec(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "invalid.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(
		build,
		`is invalid: spec.template.spec.containers[0].volumeMounts[0].name: Not found: "this-doesnt-exist"`,
	)
}

func TestInvalidPodJSON(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "invalid2.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(
		build,
		"failed parsing Kubernetes plugin: json: cannot unmarshal number into Go struct field EnvVar.podSpec.containers.env.value of type string",
	)
}

func TestEnvVariables(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "env.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Testing some env variables: set")
}

func TestImagePullBackOffFailed(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "image-pull-back-off-failed.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(build, "other job has run")
	tc.AssertLogsContain(build, "The following container images couldn't be pulled:\n * buildkite/non-existant-image:latest")
}

func TestArtifactsUploadFailedJobs(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "artifact-upload-failed-job.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	tc.AssertArtifactsContain(build, "artifact.txt")
}

func TestInterposerBuildkite(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "interposer-buildkite.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "Hello World!")
	assert.Contains(t, logs, "Goodbye World!")
	assert.NotContains(t, logs, "Hello World! echo Goodbye World!")

}

func TestInterposerVector(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "interposer-vector.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "Hello World!")
	assert.Contains(t, logs, "Goodbye World!")
}
