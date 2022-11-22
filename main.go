package main

import (
	"context"
	"log"
	"syscall"

	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
)

var pipeline *string = flag.String("pipeline", "", "pipeline to watch")

func main() {
	ctx := context.Background()
	flag.Parse()
	if *pipeline == "" {
		log.Fatalf("pipeline is required")
	}
	token := MustEnv("BUILDKITE_TOKEN")
	// TODO(bmo): generate agent tokens with the API
	agentToken := MustEnv("BUILDKITE_AGENT_TOKEN")
	org := MustEnv("BUILDKITE_ORG")

	if err := scheduler.Run(ctx, token, org, *pipeline, agentToken); err != nil {
		log.Fatalf("failed to run scheduler: %v", err)
	}
}

func MustEnv(key string) string {
	if v, ok := syscall.Getenv(key); ok {
		return v
	}

	log.Fatalf("variable '%s' cannot be found in the environment", key)
	return ""
}
