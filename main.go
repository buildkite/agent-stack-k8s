package main

import (
	"context"
	"log"
	"syscall"

	"github.com/buildkite/agent-stack-k8s/pkg/scheduler"
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
	agentToken := MustEnv("BUILDKITE_AGENT_TOKEN")
	org := MustEnv("BUILDKITE_ORG")

	scheduler.Run(ctx, token, org, *pipeline, agentToken)
}

func MustEnv(key string) string {
	if v, ok := syscall.Getenv(key); ok {
		return v
	}

	log.Fatalf("variable '%s' cannot be found in the environment", key)
	return ""
}
