package integration_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/buildkite/agent-stack-k8s/v2/internal/integration/api"
	"github.com/buildkite/roko"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/utils/ptr"
)

func TestWalkingSkeleton(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "helloworld.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}

func TestResourceClass(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "resource-class.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)

	// Configure resource classes for the test
	testCfg := cfg
	testCfg.ResourceClasses = map[string]*config.ResourceClass{
		"test": {
			Resource: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/os": "linux",
			},
		},
	}

	tc.StartController(ctx, testCfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "✅ Memory limit is correctly set to ~512MB")
	tc.AssertLogsContain(build, "✅ CPU limit is correctly set to ~500m")
}

func TestPodTemplate(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "podtemplate.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "kunanyi is a mountain")
}

func TestDefaultQueue(t *testing.T) {
	// Note: this test assumes the default queue is called "default".
	// This happens to be the case for our CI setup.
	// TODO: generalise the test to work with any name default queue once the
	// controller can function without setting the queue explicitly.
	tc := testcase{
		T:           t,
		Fixture:     "default-queue.yaml",
		Repo:        repoHTTP,
		GraphQL:     api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
		CustomQueue: "default",
	}.Init()
	ctx := context.Background()
	// Note: this shouldn't interfere with the stack running the tests, because
	// the stack uses the "kubernetes" queue.
	// We provide a custom tag to ensure this run of the test controller picks
	// up this job.
	pipeline := tc.createPipelineWithCleanup(ctx, "default", map[string]string{
		"pseudoQueue": tc.ShortPipelineName(),
	})
	tc.StartController(ctx, cfg,
		"queue=default",
		"pseudoQueue="+tc.ShortPipelineName(),
	)
	build := tc.TriggerBuild(ctx, *pipeline.GraphQLID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Hi there")
}

func TestExplicitDefaultQueue(t *testing.T) {
	// Note: this test assumes the default queue is called "default".
	// This happens to be the case for our CI setup.
	// TODO: generalise the test to work with any name default queue once the
	// controller can function without setting the queue explicitly.
	tc := testcase{
		T:           t,
		Fixture:     "explicit-default-queue.yaml",
		Repo:        repoHTTP,
		GraphQL:     api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
		CustomQueue: "default",
	}.Init()
	ctx := context.Background()
	pipeline := tc.createPipelineWithCleanup(ctx, "default", map[string]string{
		// You may wonder what this pseudoQueue is.
		// Since in this particular test case, the queue is static, without extra tag, we will be effectively limiting
		// test concurrency to 1.
		// 2 builds running at the same time might conflict with each other.
		"pseudoQueue": tc.ShortPipelineName(),
	})
	// Start a controller without queue tag, it will listen to default queue
	tc.StartController(ctx, cfg,
		"pseudoQueue="+tc.ShortPipelineName(),
	)
	build := tc.TriggerBuild(ctx, *pipeline.GraphQLID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "Hi there")
}

func TestPodSpecPatchInStep(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "podspecpatch-step.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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

func TestPodSpecPatchWithoutContainers(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "mountain.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.PodSpecPatch = &corev1.PodSpec{
		HostAliases: []corev1.HostAlias{
			{
				IP:        "127.0.0.1",
				Hostnames: []string{"agent.buildkite.localhost"},
			},
		},
	}

	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "value of MOUNTAIN is cotopaxi")
	tc.AssertHostAlias(ctx, "agent.buildkite.localhost", "127.0.0.1")
}

func TestControllerPicksUpJobsWithSubsetOfAgentTags(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "helloworld.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()

	ctx := context.Background()

	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.MaxInFlight = 1
	tc.StartController(ctx, cfg)
	buildID := tc.TriggerBuild(ctx, pipelineID).Number

	for {
		build, _, err := tc.Buildkite.Builds.Get(
			tc.Org,
			tc.PipelineName,
			strconv.Itoa(buildID),
			nil,
		)
		if err != nil {
			t.Fatalf("tc.Buildkite.Builds.Get(%q, %q, %d, nil) error = %v", tc.Org, tc.PipelineName, buildID, err)
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
			tc.Org,
			tc.PipelineName,
			strconv.Itoa(buildID),
			nil,
		)
		if err != nil {
			t.Fatalf("tc.Buildkite.Builds.Get(%q, %q, %d, nil) error = %v", tc.Org, tc.PipelineName, buildID, err)
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	time.Sleep(15 * time.Second)
	tc.AssertLogsContain(build, "Welcome to nginx!")
}

func TestExtraVolumeMounts(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "extra-volume-mounts.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}

func TestExtraVolumeMountsCommandContainers(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "extra-volume-mounts-command.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}

func TestExtraVolumeMountsSidecars(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "extra-volume-mounts-sidecar.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(build, "error looking up service account")
}

func TestEnvVariables(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "env.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(build, "Testing some env variables: set")
}

func TestImagePullBackOffFailed(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "image-pull-back-off-failed.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
	logs := tc.FetchLogs(build)
	assert.Contains(t, logs, "The following init containers failed:")
	assert.Contains(t, logs, "well this isn't going to work")
}

func TestInvalidImageRefFormat(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "invalid-image-name.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
	tc.AssertLogsContain(build, `invalid reference format "buildkite/agent:latest plus some extra junk" for container "container-0"`)
}

func TestArtifactsUploadFailedJobs(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "artifact-upload-failed-job.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
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
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	time.Sleep(15 * time.Second) // trying to reduce flakes: logs not immediately available
	assert.Contains(t, logs, "Hello World!")
	assert.Contains(t, logs, "Goodbye World!")
}

func TestCancelCheckerDeletePod(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "cancel-checker.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)

	// TriggerBuild performs this type assertion
	job := build.Jobs.Edges[0].Node.(*api.JobJobTypeCommand)
	jobName := cfg.JobPrefix + job.Uuid
	opts := metav1.ListOptions{
		LabelSelector: fields.OneTermEqualSelector("batch.kubernetes.io/job-name", jobName).String(),
	}

	// wait for pods to exist
	if err := roko.NewRetrier(
		roko.WithMaxAttempts(5),
		roko.WithStrategy(roko.Exponential(2*time.Second, 0)),
	).DoWithContext(ctx, func(r *roko.Retrier) error {
		exist, err := hasJobPod(tc, ctx, opts)
		if err != nil {
			return err
		}
		if exist != true {
			return fmt.Errorf("found no pod, waiting for pod start")
		}
		return nil
	}); err != nil {
		t.Fatalf("kubernetes.CoreV1().Pods(%q).List(ctx, %v): pods does not exist. Final error: %v",
			cfg.Namespace, opts, err)
	}

	// Cancel build
	_, err := api.BuildCancel(ctx, tc.GraphQL, api.BuildCancelInput{
		ClientMutationId: uuid.New().String(),
		Id:               build.Id,
	})
	if err != nil {
		t.Errorf("api.BuildCancel(... %q) error: %v", build.Id, err)
	}
	tc.AssertCancelled(ctx, build)

	// wait for pods to be deleted
	if err := roko.NewRetrier(
		roko.WithMaxAttempts(5),
		roko.WithStrategy(roko.Exponential(2*time.Second, 0)),
	).DoWithContext(ctx, func(r *roko.Retrier) error {
		exist, err := hasJobPod(tc, ctx, opts)
		if err != nil {
			return err
		}
		if exist {
			return fmt.Errorf("found at least one pods, waiting for deletion")
		}
		return nil
	}); err != nil {
		t.Fatalf("kubernetes.CoreV1().Pods(%q).List(ctx, %v): pods were not deleted after retries. Final error: %v",
			cfg.Namespace, opts, err)
	}
}

func hasJobPod(tc testcase, ctx context.Context, opts metav1.ListOptions) (bool, error) {
	pods, err := tc.Kubernetes.CoreV1().Pods(cfg.Namespace).List(ctx, opts)
	if err != nil {
		return false, err
	}
	return len(pods.Items) > 0, nil
}

func TestJobActiveDeadlineSeconds(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "jobactivedeadlineseconds.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertFail(ctx, build)
}

func TestHooksAndPlugins(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "hooks-and-plugins-volumes.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	cfg := cfg
	cfg.AgentConfig = &config.AgentConfig{
		HooksVolume: &corev1.Volume{
			Name: "hooks",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "integration-tests-fixture-hooks",
					},
					DefaultMode: ptr.To[int32](0o755),
				},
			},
		},
		PluginsPath: ptr.To("/my/special/plugins"),
		PluginsVolume: &corev1.Volume{
			Name: "plugins",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	logs := tc.FetchLogs(build)
	t.Logf("tc.FetchLogs(build) = %s", logs)
	for _, hook := range []string{"environment", "pre-checkout", "post-checkout", "pre-command", "post-command"} {
		want := fmt.Sprintf("Hello from the %s hook", hook)
		if !strings.Contains(logs, want) {
			t.Errorf("Logs did not contain %q", want)
		}
		want = fmt.Sprintf("Hello from the metahook %s hook", hook)
		if !strings.Contains(logs, want) {
			t.Errorf("Logs did not contain %q", want)
		}
	}
}

func TestSkipCheckoutContainer(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "skip-checkout.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
	tc.AssertLogsContain(build, "hello-skip-checkout")
}

func TestImageAttribute(t *testing.T) {
	tc := testcase{
		T:       t,
		Fixture: "image-attribute.yaml",
		Repo:    repoHTTP,
		GraphQL: api.NewGraphQLClient(cfg.BuildkiteToken, cfg.GraphQLEndpoint),
	}.Init()
	ctx := context.Background()
	pipelineID := tc.PrepareQueueAndPipelineWithCleanup(ctx)
	tc.StartController(ctx, cfg)
	build := tc.TriggerBuild(ctx, pipelineID)
	tc.AssertSuccess(ctx, build)
}
