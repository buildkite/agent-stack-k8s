package integration_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/google/uuid"
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "value of MOUNTAIN is cotopaxi")
}

func TestPodSpecPatchAllowsPatchingCommandContainerCommands(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "podspecpatch-command-step.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()

	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "i love quito")
}

func TestPodSpecPatchRejectsPatchingAgentContainerCommand(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "podspecpatch-agent.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()

	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertFail(ctx, build)
	tc.AssertLogsContain(build, scheduler.ErrNoCommandModification.Error())
}

func TestPodSpecPatchInController(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "mountain.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
			strconv.Itoa(buildID),
			nil,
		)
		if err != nil {
			t.Fatalf("tc.Buildkite.Builds.Get(%q, %q, %d, nil) error = %v", cfg.Org, tc.PipelineName, buildID, err)
		}

		switch *build.State {
		case "running":
			if got, want := *build.Pipeline.RunningJobsCount, cfg.MaxInFlight; got > want {
				t.Fatalf("*build.Pipeline.RunningJobsCount = %d, want <= %d", got, want)
			}

		case "passed":
			return

		case "scheduled":
			t.Log("waiting for build to start")

		default:
			t.Fatalf("unexpected build state: %v", *build.State)
		}

		// Don't want to hammer the API.
		time.Sleep(500 * time.Millisecond)
	}
}

func TestMaxInFlightUnlimited(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "parallel.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()

	ctx := context.Background()

	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.MaxInFlight = 0 // unlimited
	tc.StartController(ctx, cfg)
	buildID := tc.TriggerBuild(ctx, pipelineID).Number

	maxRunningJobs := 0
fetchBuildStateLoop:
	for {
		build, _, err := tc.Buildkite.Builds.Get(
			cfg.Org,
			tc.PipelineName,
			strconv.Itoa(buildID),
			nil,
		)
		if err != nil {
			t.Fatalf("tc.Buildkite.Builds.Get(%q, %q, %d, nil) error = %v", cfg.Org, tc.PipelineName, buildID, err)
		}

		switch *build.State {
		case "running":
			runningJobs := 0
			for _, job := range build.Jobs {
				if *job.State == "running" {
					runningJobs++
				}
			}
			t.Logf("running, runningJobs: %d", runningJobs)
			maxRunningJobs = max(maxRunningJobs, runningJobs)

		case "passed":
			break fetchBuildStateLoop

		case "scheduled":
			t.Log("waiting for build to start")

		default:
			t.Fatalf("unexpected build state: %v", *build.State)
		}

		// Don't want to hammer the API.
		time.Sleep(500 * time.Millisecond)
	}

	// All jobs _can_ run at once... but there's no guarantee that will
	// happen. There's no "fence" that could be used to block all the jobs
	// until they're all running. Until we have something like that, this can
	// flake.
	if maxRunningJobs != 4 {
		t.Errorf("maxRunningJobs = %d, want 4", maxRunningJobs)
	}
}

func TestSidecars(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "sidecars.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	time.Sleep(5 * time.Second)
	tc.AssertLogsContain(build, "Welcome to nginx!")
}

func TestExtraVolumeMounts(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "extra-volume-mounts.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(
		build,
		"failed parsing Kubernetes plugin: json: cannot unmarshal number into Go struct field EnvVar.podSpec.containers.env.value of type string",
	)
}

func TestMissingServiceAccount(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "missing-service-account.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(build, "error looking up service account")
}

func TestEnvVariables(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "env.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(build, "Testing some env variables: set")
}

func TestImagePullBackOffFailed(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "image-pull-back-off-failed.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "other job has run")
	assert.Contains(t, logs, "The following images could not be pulled or were unavailable:\n")
	assert.Contains(t, logs, `"buildkite/non-existant-image:latest"`)
	assert.Contains(t, logs, "ImagePullBackOff")
}

func TestPullPolicyNeverMissingImage(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "never-pull.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "The following images could not be pulled or were unavailable:\n")
	assert.Contains(t, logs, `"buildkite/agent-extreme:never"`)
	assert.Contains(t, logs, "ErrImageNeverPull")
}

func TestBrokenInitContainer(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "broken-init-container.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "The following init containers failed:")
	assert.Contains(t, logs, "well this isn't going to work")
}

func TestInvalidImageRefFormat(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "invalid-image-name.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(build, `invalid reference format "buildkite/agent:latest plus some extra junk" for container "container-0"`)
}

func TestArtifactsUploadFailedJobs(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "artifact-upload-failed-job.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	assert.Contains(t, logs, "Hello World!")
	assert.Contains(t, logs, "Goodbye World!")
}

func TestCancelCheckerEvictsPod(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "cancel-checker.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	time.Sleep(10 * time.Second)

	_, err := api.BuildCancel(ctx, tc.GraphQL, api.BuildCancelInput{
		ClientMutationId: uuid.New().String(),
		Id:               build.Id,
	})
	if err != nil {
		t.Errorf("api.BuildCancel(... %q) error: %v", build.Id, err)
	}
	tc.AssertCancelled(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	logs := tc.FetchLogs(build)
	if strings.Contains(logs, "Received cancellation signal, interrupting") {
		t.Error("The agent ran and handled cancellation")
	}
}

func TestJobActiveDeadlineSeconds(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "jobactivedeadlineseconds.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(5 * time.Second) // trying to reduce flakes: logs not immediately available
	logs := tc.FetchLogs(build)
	if strings.Contains(logs, "Received cancellation signal, interrupting") {
		t.Error("The agent ran and handled cancellation")
	}
}
