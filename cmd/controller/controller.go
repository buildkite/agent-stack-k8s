package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/linter"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/version"
	"github.com/buildkite/agent-stack-k8s/v2/internal/monitor"
	"github.com/buildkite/agent-stack-k8s/v2/internal/scheduler"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
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

	// not in the config file
	cmd.Flags().String("agent-token-secret", "buildkite-agent-token", "name of the Buildkite agent token secret")
	cmd.Flags().String("buildkite-token", "", "Buildkite API token with GraphQL scopes")

	// in the config file
	cmd.Flags().String("org", "", "Buildkite organization name to watch")
	cmd.Flags().String("image", api.DefaultAgentImage, "The image to use for the Buildkite agent")
	cmd.Flags().StringSlice(
		"tags", []string{"queue=kubernetes"}, `A comma-separated list of tags for the agent (for example, "linux" or "mac,xcode=8")`,
	)
	cmd.Flags().String("namespace", api.DefaultNamespace, "kubernetes namespace to create resources in")
	cmd.Flags().Bool("debug", false, "debug logs")
	cmd.Flags().Int("max-in-flight", 25, "max jobs in flight, 0 means no max")
	cmd.Flags().Duration("job-ttl", 10*time.Minute, "time to retain kubernetes jobs after completion")
	cmd.Flags().String(
		"cluster-uuid", "", "UUID of the Cluster. The agent token must be for the Cluster.",
	)
	cmd.Flags().String("profiler-address", "", "Bind address to expose the pprof profiler (e.g. localhost:6060)")
}

func ParseConfig(cmd *cobra.Command, args []string) (api.Config, error) {
	var cfg api.Config
	if err := cmd.Flags().Parse(args); err != nil {
		return cfg, fmt.Errorf("failed to parse flags: %w", err)
	}

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
	if err := validate.Struct(cfg); err != nil {
		return cfg, fmt.Errorf("failed to validate config: %w", err)
	}

	return cfg, nil
}

var (
	english  = en.New()
	uni      = ut.New(english, english)
	validate = validator.New()
	trans, _ = uni.GetTranslator("en")
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent-stack-k8s",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			ctx := signals.SetupSignalHandler()

			cfg, err := ParseConfig(cmd, args)
			if err != nil {
				var errs validator.ValidationErrors
				if errors.As(err, &errs) {
					for _, e := range errs {
						log.Println(e.Translate(trans))
					}
					os.Exit(1)
				}
				log.Fatalf("failed to parse config: %v", err)
			}

			config := zap.NewDevelopmentConfig()
			if cfg.Debug {
				config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
			} else {
				config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
			}

			logger := zap.Must(config.Build())
			logger.Info("configuration loaded", zap.Object("config", cfg))

			clientConfig := restconfig.GetConfigOrDie()
			k8sClient, err := kubernetes.NewForConfig(clientConfig)
			if err != nil {
				logger.Error("failed to create clientset", zap.Error(err))
			}

			Run(ctx, logger, k8sClient, cfg)
		},
	}
	addFlags(cmd)
	cmd.AddCommand(linter.New())
	cmd.AddCommand(version.New())
	if err := en_translations.RegisterDefaultTranslations(validate, trans); err != nil {
		log.Fatalf("failed to register translations: %v", err)
	}

	return cmd
}

func Run(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface, cfg api.Config) {
	if cfg.ProfilerAddress != "" {
		logger.Info("profiler listening for requests")
		go func() {
			srv := http.Server{Addr: cfg.ProfilerAddress, ReadHeaderTimeout: 2 * time.Second}
			if err := srv.ListenAndServe(); err != nil {
				logger.Error("problem running profiler server", zap.Error(err))
			}
		}()
	}

	m, err := monitor.New(logger.Named("monitor"), k8sClient, cfg)
	if err != nil {
		logger.Fatal("failed to create monitor", zap.Error(err))
	}

	sched := scheduler.New(logger.Named("scheduler"), k8sClient, cfg)
	limiter := scheduler.NewLimiter(logger.Named("limiter"), sched, cfg.MaxInFlight)

	informerFactory, err := scheduler.NewInformerFactory(k8sClient, cfg.Tags)
	if err != nil {
		logger.Fatal("failed to create informer", zap.Error(err))
	}

	if err := limiter.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register limiter informer", zap.Error(err))
	}

	completions := scheduler.NewPodCompletionWatcher(logger.Named("completions"), k8sClient)
	if err := completions.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register completions informer", zap.Error(err))
	}

	imagePullBackOffWatcher := scheduler.NewImagePullBackOffWatcher(
		logger.Named("imagePullBackoffWatcher"),
		k8sClient,
		cfg,
	)
	if err := imagePullBackOffWatcher.RegisterInformer(ctx, informerFactory); err != nil {
		logger.Fatal("failed to register imagePullBackoffWatcher informer", zap.Error(err))
	}

	select {
	case <-ctx.Done():
		logger.Info("controller exiting", zap.Error(ctx.Err()))
	case err := <-m.Start(ctx, limiter):
		logger.Info("monitor failed", zap.Error(err))
	}
}
