package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

var config *string = flag.StringP("config", "f", "", "config file path")

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

func ParseConfig() (api.Config, error) {
	var cfg api.Config
	flag.Parse()
	if err := viper.BindPFlags(flag.CommandLine); err != nil {
		return cfg, fmt.Errorf("failed to bind flags: %w", err)
	}
	if *config == "" {
		*config = os.Getenv("CONFIG")
	}
	viper.SetConfigFile(*config)
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	if err := viper.ReadInConfig(); err != nil {
		if !errors.As(err, &viper.ConfigFileNotFoundError{}) {
			return cfg, fmt.Errorf("failed to read config: %w", err)
		}
	}

	if err := viper.Unmarshal(&cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config: %w", err)
	}
	return cfg, nil
}

func Run(ctx context.Context, k8sClient kubernetes.Interface, cfg api.Config) {
	config := zap.NewDevelopmentConfig()
	if cfg.Debug {
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	log := zap.Must(config.Build())

	if cfg.MaxInFlight < 0 {
		log.Fatal("max-in-flight must be greater than or equal to zero")
	}

	monitor, err := monitor.New(ctx, log.Named("monitor"), k8sClient, cfg)
	if err != nil {
		log.Fatal("failed to create monitor", zap.Error(err))
	}
	if err := scheduler.Run(ctx, zap.L().Named("scheduler"), monitor, k8sClient, cfg); err != nil {
		log.Fatal("failed to run scheduler", zap.Error(err))
	}
}
