package controller

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/alecthomas/kong"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/linter"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/version"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"k8s.io/client-go/kubernetes"
	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	english  = en.New()
	uni      = ut.New(english, english)
	validate = validator.New()
	trans, _ = uni.GetTranslator("en")
)

func init() {
	if err := en_translations.RegisterDefaultTranslations(validate, trans); err != nil {
		log.Fatalf("failed to register translations: %v", err)
	}
}

// Run is the main entry point for the controller.
func Run() error {
	// Check if first arg is a subcommand
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "lint":
			return linter.Run()
		case "version":
			return version.Run()
		}
	}

	cfg, err := BuildConfigFromArgs(os.Args[1:])
	if err != nil {
		var errs validator.ValidationErrors
		if errors.As(err, &errs) {
			for _, e := range errs {
				log.Println(e.Translate(trans))
			}
		}
		return err
	}

	return runController(cfg)
}

// BuildConfigFromArgs parses CLI args and env vars, then builds config.
// Config file can be specified via --config flag or CONFIG env var.
// Priority: defaults < config file < environment variables < CLI args
func BuildConfigFromArgs(args []string) (*config.Config, error) {
	cli := &CLI{}
	parser, err := kong.New(
		cli,
		kong.Name("agent-stack-k8s"),
		kong.Description("Buildkite Agent Stack for Kubernetes"),
		kong.DefaultEnvars(""),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.UsageOnError(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	_, err = parser.Parse(args)
	if err != nil {
		parser.FatalIfErrorf(err)
	}

	return buildConfig(cli)
}

// buildConfig builds the config from CLI values with correct precedence:
// defaults < config file < env vars/CLI flags.
// Kong has already applied env vars and CLI flags to the cli struct.
func buildConfig(cli *CLI) (*config.Config, error) {
	// Step 1: Start with defaults
	cfg := newConfigWithDefaults()

	// Step 2: Apply config file values (overrides defaults)
	if cli.ConfigFile != "" {
		fileCfg, keys, err := loadConfigFile(cli.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		mergeConfigFromFile(cfg, fileCfg, keys)
	}

	// Step 3: Apply CLI/env values (overrides config file)
	applyCLIOverrides(cfg, cli)

	// Step 4: Validate
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return cfg, nil
}

func runController(cfg *config.Config) error {
	ctx := signals.SetupSignalHandler()

	level := slog.LevelInfo
	if cfg.LogLevel != "" {
		if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
			return fmt.Errorf("invalid log level %q: %w", cfg.LogLevel, err)
		}
	}

	if cfg.Debug {
		level = slog.LevelDebug
	}

	var handler slog.Handler
	switch cfg.LogFormat {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})

	case "logfmt", "":
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			AddSource: true,
			Level:     level,
			NoColor:   cfg.NoColor || !isatty.IsTerminal(os.Stdout.Fd()),
		})

	default:
		return fmt.Errorf("unknown log format: %s", cfg.LogFormat)
	}

	logger := slog.New(handler)
	logger.Debug("configuration loaded", "config", cfg)

	clientConfig := restconfig.GetConfigOrDie()
	clientConfig.QPS = float32(cfg.K8sClientRateLimiterQPS)
	clientConfig.Burst = cfg.K8sClientRateLimiterBurst

	// Default to Protobuf encoding for API responses, support fallback to JSON
	clientConfig.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	clientConfig.ContentType = "application/vnd.kubernetes.protobuf"

	k8sClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		logger.Error("failed to create clientset", "error", err)
		return err
	}

	controller.Run(ctx, logger, k8sClient, cfg)

	return nil
}
