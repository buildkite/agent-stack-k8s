package main

import (
	"errors"
	"log"
	"strings"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"

	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var config *string = flag.StringP("config", "f", "", "config file path")

type Config struct {
	AgentTokenSecret *string `mapstructure:"agent-token-secret"`
	BuildkiteToken   *string `mapstructure:"buildkite-token"`
	Debug            bool
	Image            *string
	JobTTL           time.Duration `mapstructure:"job-ttl"`
	MaxInFlight      *int          `mapstructure:"max-in-flight"`
	Namespace        *string
	Org              *string
	Tags             []string
}

func init() {
	flag.String("buildkite-token", "", "Buildkite API token with GraphQL scopes")
	flag.String("org", "", "Buildkite organization name to watch")
	flag.String("image", api.DefaultAgentImage, "The image to use for the Buildkite agent")
	flag.StringSlice("tags", []string{"queue=kubernetes"}, `A comma-separated list of tags for the agent (for example, "linux" or "mac,xcode=8")`)
	flag.String("namespace", api.DefaultNamespace, "kubernetes namespace to create resources in")
	flag.Bool("debug", false, "debug logs")
	flag.Int("max-in-flight", 1, "max jobs in flight, 0 means no max")
	flag.Duration("job-ttl", 10*time.Minute, "time to retain kubernetes jobs after completion")
	flag.String("agent-token-secret", "buildkite-agent-token", "name of the Buildkite agent token secret")
}

func main() {
	flag.Parse()
	if err := viper.BindPFlags(flag.CommandLine); err != nil {
		log.Fatalf("failed to bind flags: %v", err)
	}
	viper.SetConfigFile(*config)
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	if err := viper.ReadInConfig(); err != nil {
		if !errors.As(err, &viper.ConfigFileNotFoundError{}) {
			log.Fatalf("failed to read config: %v", err)
		}
	}
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	initLogger(cfg.Debug)
	log := zap.L()

	if *cfg.MaxInFlight < 0 {
		log.Fatal("max-in-flight must be greater than or equal to zero")
	}

	ctx := signals.SetupSignalHandler()

	clientConfig := restconfig.GetConfigOrDie()
	k8sClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		log.Fatal("failed to create clienset", zap.Error(err))
	}
	monitor, err := monitor.New(ctx, log.Named("monitor"), k8sClient, monitor.Config{
		Namespace:   *cfg.Namespace,
		Org:         *cfg.Org,
		Token:       *cfg.BuildkiteToken,
		MaxInFlight: *cfg.MaxInFlight,
		Tags:        cfg.Tags,
	})
	if err != nil {
		zap.L().Fatal("failed to create monitor", zap.Error(err))
	}
	if err := scheduler.Run(ctx, zap.L().Named("scheduler"), monitor, k8sClient, scheduler.Config{
		Namespace:        *cfg.Namespace,
		AgentTokenSecret: *cfg.AgentTokenSecret,
		JobTTL:           cfg.JobTTL,
		AgentImage:       *cfg.Image,
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
