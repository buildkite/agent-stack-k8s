package main

import (
	"context"
	"log"
	"syscall"

	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
)

var pipeline *string = flag.String("pipeline", "", "pipeline to watch")
var debug *bool = flag.Bool("debug", false, "debug logs")
var maxInFlight *int = flag.Int("max-in-flight", 1, "max jobs in flight")

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
	initLogger(*debug)

	monitor := monitor.New(zap.L().Named("monitor"), token)
	if err := scheduler.Run(ctx, zap.L().Named("scheduler"), monitor, scheduler.Config{
		Org:         org,
		Pipeline:    *pipeline,
		AgentToken:  agentToken,
		DeletePods:  true,
		MaxInFlight: *maxInFlight,
	}); err != nil {
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

func initLogger(debug bool) {
	config := zap.NewDevelopmentConfig()
	if debug {
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	logger := zap.Must(config.Build())
	zap.ReplaceGlobals(logger)
}
