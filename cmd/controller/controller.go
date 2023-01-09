package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/cmd/linter"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var configFile string

func addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&configFile, "config", "f", "", "config file path")
	cmd.Flags().String("buildkite-token", "", "Buildkite API token with GraphQL scopes")
	cmd.Flags().String("org", "", "Buildkite organization name to watch")
	cmd.Flags().String("image", api.DefaultAgentImage, "The image to use for the Buildkite agent")
	cmd.Flags().StringSlice("tags", []string{"queue=kubernetes"}, `A comma-separated list of tags for the agent (for example, "linux" or "mac,xcode=8")`)
	cmd.Flags().String("namespace", api.DefaultNamespace, "kubernetes namespace to create resources in")
	cmd.Flags().Bool("debug", false, "debug logs")
	cmd.Flags().Int("max-in-flight", 1, "max jobs in flight, 0 means no max")
	cmd.Flags().Duration("job-ttl", 10*time.Minute, "time to retain kubernetes jobs after completion")
	cmd.Flags().String("agent-token-secret", "buildkite-agent-token", "name of the Buildkite agent token secret")
}

func ParseConfig(cmd *cobra.Command, args []string) (api.Config, error) {
	var cfg api.Config
	cmd.Flags().Parse(args)
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return cfg, fmt.Errorf("failed to bind flags: %w", err)
	}
	if configFile == "" {
		configFile = os.Getenv("CONFIG")
	}
	viper.SetConfigFile(configFile)
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
	if err := validator.New().Struct(cfg); err != nil {
		return cfg, fmt.Errorf("failed to validate config: %w", err)
	}

	return cfg, nil
}

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent-stack-k8s",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			ctx := signals.SetupSignalHandler()

			clientConfig := restconfig.GetConfigOrDie()
			k8sClient, err := kubernetes.NewForConfig(clientConfig)
			if err != nil {
				log.Fatalf("failed to create clientset: %v", err)
			}
			cfg, err := ParseConfig(cmd, args)
			if err != nil {
				log.Fatalf("failed to parse config: %v", err)
			}
			Run(ctx, k8sClient, cfg)
		},
	}
	addFlags(cmd)
	cmd.AddCommand(linter.New())

	return cmd
}

func Run(ctx context.Context, k8sClient kubernetes.Interface, cfg api.Config) {
	config := zap.NewDevelopmentConfig()
	if cfg.Debug {
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	log := zap.Must(config.Build())
	log.Info("configuration loaded", zap.Object("config", cfg))

	monitor, err := monitor.New(ctx, log.Named("monitor"), k8sClient, cfg)
	if err != nil {
		log.Fatal("failed to create monitor", zap.Error(err))
	}
	if err := scheduler.Run(ctx, log.Named("scheduler"), monitor, k8sClient, cfg); err != nil {
		log.Fatal("failed to run scheduler", zap.Error(err))
	}
}
