package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
)

var pipeline *string = flag.String("pipeline", "", "pipeline to watch")
var debug *bool = flag.Bool("debug", false, "debug logs")
var maxInFlight *int = flag.Int("max-in-flight", 1, "max jobs in flight, 0 means no max")
var jobTTL *time.Duration = flag.Duration("job-ttl", 10*time.Minute, "time to retain kubernetes jobs after completion")

func main() {
	flag.Parse()
	if *pipeline == "" {
		log.Fatalf("pipeline is required")
	}
	if *maxInFlight < 0 {
		log.Fatalf("max-in-flight must be greater than or equal to zero")
	}
	token := MustEnv("BUILDKITE_TOKEN")
	// TODO(bmo): generate agent tokens with the API
	agentToken := MustEnv("BUILDKITE_AGENT_TOKEN")
	org := MustEnv("BUILDKITE_ORG")
	initLogger(*debug)

	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cancel()
	}()

	monitor, err := monitor.New(ctx, zap.L().Named("monitor"), monitor.Config{
		Org:         org,
		Pipeline:    *pipeline,
		Token:       token,
		MaxInFlight: *maxInFlight,
	})
	if err != nil {
		zap.L().Fatal("failed to create monitor", zap.Error(err))
	}
	if err := scheduler.Run(ctx, zap.L().Named("scheduler"), monitor, scheduler.Config{
		AgentToken: agentToken,
		JobTTL:     *jobTTL,
	}); err != nil {
		zap.L().Fatal("failed to run scheduler", zap.Error(err))
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
