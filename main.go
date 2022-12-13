package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var debug *bool = flag.Bool("debug", false, "debug logs")
var maxInFlight *int = flag.Int("max-in-flight", 1, "max jobs in flight, 0 means no max")
var jobTTL *time.Duration = flag.Duration("job-ttl", 10*time.Minute, "time to retain kubernetes jobs after completion")
var agentTokenSecret *string = flag.String("agent-token-secret", "buildkite-agent-token", "name of the Buildkite agent token secret")
var ns *string = flag.String("namespace", api.DefaultNamespace, "kubernetes namespace to create resources in")
var tags *[]string = flag.StringSlice("tags", []string{"queue=kubernetes"}, `A comma-separated list of tags for the agent (for example, "linux" or "mac,xcode=8")`)

func main() {
	flag.Parse()
	if *maxInFlight < 0 {
		log.Fatalf("max-in-flight must be greater than or equal to zero")
	}
	token := MustEnv("BUILDKITE_TOKEN")
	org := MustEnv("BUILDKITE_ORG")
	initLogger(*debug)
	log := zap.L()

	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cancel()
	}()

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, nil)
	clientConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		log.Fatal("failed to create client config", zap.Error(err))
	}

	k8sClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		log.Fatal("failed to create clienset", zap.Error(err))
	}

	monitor, err := monitor.New(ctx, log.Named("monitor"), k8sClient, monitor.Config{
		Namespace:   *ns,
		Org:         org,
		Tags:        *tags,
		Token:       token,
		MaxInFlight: *maxInFlight,
	})
	if err != nil {
		zap.L().Fatal("failed to create monitor", zap.Error(err))
	}
	if err := scheduler.Run(ctx, zap.L().Named("scheduler"), monitor, k8sClient, scheduler.Config{
		Namespace:        *ns,
		AgentTokenSecret: *agentTokenSecret,
		JobTTL:           *jobTTL,
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
