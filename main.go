package main

import (
	"log"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var debug *bool = flag.Bool("debug", false, "debug logs")
var maxInFlight *int = flag.Int("max-in-flight", 1, "max jobs in flight, 0 means no max")
var jobTTL *time.Duration = flag.Duration("job-ttl", 10*time.Minute, "time to retain kubernetes jobs after completion")
var agentTokenSecret *string = flag.String("agent-token-secret", "buildkite-agent-token", "name of the Buildkite agent token secret")
var ns *string = flag.String("namespace", api.DefaultNamespace, "kubernetes namespace to create resources in")
var tags *[]string = flag.StringSlice("tags", []string{"queue=kubernetes"}, `A comma-separated list of tags for the agent (for example, "linux" or "mac,xcode=8")`)
var agentImage *string = flag.String("image", api.DefaultAgentImage, "The image to use for the Buildkite agent")

func main() {
	flag.Parse()
	if *maxInFlight < 0 {
		log.Fatalf("max-in-flight must be greater than or equal to zero")
	}
	token := MustEnv("BUILDKITE_TOKEN")
	org := MustEnv("BUILDKITE_ORG")
	initLogger(*debug)
	log := zap.L()

	ctx := signals.SetupSignalHandler()

	clientConfig := restconfig.GetConfigOrDie()
	k8sClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		log.Fatal("failed to create clienset", zap.Error(err))
	}
	jobLister := api.NewJobListerOrDie(ctx, k8sClient, *tags...)

	monitor := monitor.New(ctx, log.Named("monitor"), jobLister, monitor.Config{
		Namespace:   *ns,
		Org:         org,
		Token:       token,
		MaxInFlight: *maxInFlight,
		Tags:        *tags,
	})
	if err != nil {
		zap.L().Fatal("failed to create monitor", zap.Error(err))
	}
	if err := scheduler.Run(ctx, zap.L().Named("scheduler"), monitor, k8sClient, scheduler.Config{
		Namespace:        *ns,
		AgentTokenSecret: *agentTokenSecret,
		JobTTL:           *jobTTL,
		AgentImage:       *agentImage,
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
