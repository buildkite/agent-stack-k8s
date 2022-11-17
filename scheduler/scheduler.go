package scheduler

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/sanity-io/litter"
)

func Run(ctx context.Context, token, org, pipeline, agentToken string) {
	graphqlClient := api.NewClient(token)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			buildsResponse, err := api.GetBuildsForPipelineBySlug(ctx, graphqlClient, fmt.Sprintf("%s/%s", org, pipeline))
			if err != nil {
				log.Fatalf("failed to fetch builds for pipeline: %v", err)
			}
			for _, build := range buildsResponse.Pipeline.Builds.Edges {
				for _, job := range build.Node.Jobs.Edges {
					switch job := job.Node.(type) {
					case *api.GetBuildsForPipelineBySlugPipelineBuildsBuildConnectionEdgesBuildEdgeNodeBuildJobsJobConnectionEdgesJobEdgeNodeJobTypeCommand:
						cmd := exec.Command(
							"kubectl", "run", "agent",
							"--image=buildkite/agent:latest",
							fmt.Sprintf(`--env=BUILDKITE_AGENT_ACQUIRE_JOB=%s`, job.Uuid),
							fmt.Sprintf(`--env=BUILDKITE_AGENT_TOKEN=%s`, agentToken),
							"--attach",
							"--rm",
						)
						cmd.Stdout = os.Stdout
						cmd.Stderr = os.Stderr
						if err := cmd.Run(); err != nil {
							log.Fatalf("failed to run agent: %v", err)
						}
					default:
						log.Fatalf("received unknown job type: %v", litter.Sdump(job))
					}
				}
			}
		}
		time.Sleep(time.Second)
	}
}
