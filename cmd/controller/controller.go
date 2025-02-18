package controller

import (
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/linter"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/version"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var configFile string

func AddConfigFlags(cmd *cobra.Command) {
	// the config file flag
	cmd.Flags().StringVarP(&configFile, "config", "f", "", "config file path")

	// not in the config file
	cmd.Flags().String(
		"agent-token-secret",
		"buildkite-agent-token",
		"name of the Buildkite agent token secret",
	)
	cmd.Flags().String("buildkite-token", "", "Buildkite API token with GraphQL scopes")

	// in the config file
	cmd.Flags().String("org", "", "Buildkite organization name to watch")
	cmd.Flags().String(
		"image",
		config.DefaultAgentImage,
		"The image to use for the Buildkite agent",
	)
	cmd.Flags().StringSlice(
		"tags",
		[]string{"queue=kubernetes"},
		`A comma-separated list of agent tags. The "queue" tag must be unique (e.g. "queue=kubernetes,os=linux")`,
	)
	cmd.Flags().String(
		"namespace",
		config.DefaultNamespace,
		"kubernetes namespace to create resources in",
	)
	cmd.Flags().Bool("debug", false, "debug logs")
	cmd.Flags().Int("max-in-flight", 25, "max jobs in flight, 0 means no max")
	cmd.Flags().Duration(
		"job-ttl",
		10*time.Minute,
		"time to retain kubernetes jobs after completion",
	)
	cmd.Flags().Int(
		"job-active-deadline-seconds",
		21600,
		"maximum number of seconds a kubernetes job is allowed to run before terminating all pods and failing job",
	)
	cmd.Flags().Duration(
		"poll-interval",
		time.Second,
		"time to wait between polling for new jobs (minimum 1s); note that increasing this causes jobs to be slower to start",
	)
	cmd.Flags().String(
		"cluster-uuid",
		"",
		"UUID of the Buildkite Cluster. The agent token must be for the Buildkite Cluster.",
	)
	cmd.Flags().String(
		"profiler-address",
		"",
		"Bind address to expose the pprof profiler (e.g. localhost:6060)",
	)
	cmd.Flags().Uint16(
		"prometheus-port",
		0,
		"Bind port to expose Prometheus /metrics; 0 disables it",
	)
	cmd.Flags().String("graphql-endpoint", "", "Buildkite GraphQL endpoint URL")

	cmd.Flags().Duration(
		"stale-job-data-timeout",
		config.DefaultStaleJobDataTimeout,
		"Duration after querying jobs in Buildkite that the data is considered valid",
	)
	cmd.Flags().Int(
		"job-creation-concurrency",
		config.DefaultJobCreationConcurrency,
		"Number of concurrent goroutines to run for converting Buildkite jobs into Kubernetes jobs",
	)
	cmd.Flags().Int(
		"k8s-client-rate-limiter-qps",
		config.DefaultK8sClientRateLimiterQPS,
		"The QPS value of the K8s client rate limiter.",
	)
	cmd.Flags().Int(
		"k8s-client-rate-limiter-burst",
		config.DefaultK8sClientRateLimiterBurst,
		"The burst value of the K8s client rate limiter.",
	)
	cmd.Flags().Duration(
		"image-pull-backoff-grace-period",
		config.DefaultImagePullBackOffGracePeriod,
		"Duration after starting a pod that the controller will wait before considering cancelling a job due to ImagePullBackOff (e.g. when the podSpec specifies container images that cannot be pulled)",
	)
	cmd.Flags().Duration(
		"job-cancel-checker-poll-interval",
		config.DefaultJobCancelCheckerPollInterval,
		"Controls the interval between job state queries while a pod is still Pending",
	)
	cmd.Flags().Duration(
		"empty-job-grace-period",
		config.DefaultEmptyJobGracePeriod,
		"Duration after starting a Kubernetes job that the controller will wait before considering failing the job due to a missing pod (e.g. when the podSpec specifies a missing service account)",
	)
	cmd.Flags().String(
		"default-image-pull-policy",
		"",
		"Configures a default image pull policy for containers that do not specify a pull policy and non-init containers created by the stack itself",
	)
	cmd.Flags().String(
		"default-image-check-pull-policy",
		"",
		"Sets a default PullPolicy for image-check init containers, used if an image pull policy is not set for the corresponding container in a podSpec or podSpecPatch",
	)
	cmd.Flags().Bool(
		"prohibit-kubernetes-plugin",
		false,
		"Causes the controller to prohibit the kubernetes plugin specified within jobs (pipeline YAML) - enabling this causes jobs with a kubernetes plugin to fail, preventing the pipeline YAML from having any influence over the podSpec",
	)
	cmd.Flags().Int(
		"graphql-results-limit",
		config.DefaultGraphQLResultsLimit,
		"Sets the amount of results returned by GraphQL queries when retreiving Jobs to be Scheduled")
}

// ReadConfigFromFileArgsAndEnv reads the config from the file, env and args in that order.
// an excaption is the path to the config file which is read from the args and env only.
func ReadConfigFromFileArgsAndEnv(cmd *cobra.Command, args []string) (*viper.Viper, error) {
	// First parse the flags so we can settle on the config file
	if err := cmd.Flags().Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Settle on the config file
	if configFile == "" {
		configFile = os.Getenv("CONFIG")
	}

	// By default Viper unmarshals a key like "a.b.c" as nested maps:
	//   map[string]any{"a": map[string]any{"b": map[string]any{"c": ... }}}
	// which is frustrating, because `.` is commonly used in Kubernetes labels,
	// annotations, and node selector keys (they tend to use domain names to
	// "namespace" keys). So change Viper's delimiter to`::`.
	v := viper.NewWithOptions(
		viper.KeyDelimiter("::"),
		viper.EnvKeyReplacer(strings.NewReplacer("-", "_")),
	)
	v.SetConfigFile(configFile)

	// Bind the flags to the viper instance, but only those that can appear in the config file.
	errs := []error{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		switch f.Name {
		case "config", "help":
			// skip
		default:
			if err := v.BindPFlag(f.Name, f); err != nil {
				errs = append(errs, fmt.Errorf("failed to bind flag %s: %w", f.Name, err))
			}
		}
	})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if !errors.As(err, &viper.ConfigFileNotFoundError{}) {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	return v, nil
}

var resourceQuantityType = reflect.TypeOf(resource.Quantity{})
var intOrStringType = reflect.TypeOf(intstr.IntOrString{})

// This mapstructure.DecodeHookFunc is needed to decode kubernetes objects (as
// used in podSpecs) properly. Without this, viper (which uses mapstructure) doesn't
// e.g. know how to put a string (e.g. "100m") into a "map" (resource.Quantity) and
// will error out.
func decodeKubeSpecials(f, t reflect.Type, data any) (any, error) {
	switch t {
	case resourceQuantityType:
		switch f.Kind() {
		case reflect.String:
			return resource.ParseQuantity(data.(string))
		case reflect.Float64:
			return resource.ParseQuantity(strconv.FormatFloat(data.(float64), 'f', -1, 64))
		case reflect.Float32:
			return resource.ParseQuantity(strconv.FormatFloat(float64(data.(float32)), 'f', -1, 32))
		case reflect.Int:
			return resource.ParseQuantity(strconv.Itoa(data.(int)))
		default:
			return nil, fmt.Errorf("invalid resource quantity: %v", data)
		}
	case intOrStringType:
		switch f.Kind() {
		case reflect.String:
			return intstr.FromString(data.(string)), nil
		case reflect.Int:
			return intstr.FromInt(data.(int)), nil
		default:
			return nil, fmt.Errorf("invalid int/string: %v", data)
		}
	default:
		return data, nil
	}

}

// This viper.DecoderConfigOption is needed to make mapstructure (used by viper)
// use the same struct tags that the k8s libraries provide.
func useJSONTagForDecoder(c *mapstructure.DecoderConfig) {
	c.TagName = "json"
}

// ParseAndValidateConfig parses the config into a struct and validates the values.
func ParseAndValidateConfig(v *viper.Viper) (*config.Config, error) {
	// We want to let the user know if they have any extra fields, so use UnmarshalExact.
	// The user likely expects every part of their config to be meaningful, so if some of it is
	// ignored in parsing, they almost certainly want to know about it.
	cfg := &config.Config{}
	// This decode hook = the default Viper decode hooks + decodeKubeSpecials
	// (Setting this option overrides the default.)
	decodeHook := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		decodeKubeSpecials,
		config.StringToInterposer,
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))
	if err := v.UnmarshalExact(cfg, useJSONTagForDecoder, decodeHook); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := validate.Struct(cfg); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	if cfg.PodSpecPatch != nil {
		for _, c := range cfg.PodSpecPatch.Containers {
			if len(c.Command) != 0 || len(c.Args) != 0 {
				return nil, scheduler.ErrNoCommandModification
			}
		}
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
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := signals.SetupSignalHandler()

			v, err := ReadConfigFromFileArgsAndEnv(cmd, args)
			if err != nil {
				return err
			}

			cfg, err := ParseAndValidateConfig(v)
			if err != nil {
				var errs validator.ValidationErrors
				if errors.As(err, &errs) {
					for _, e := range errs {
						log.Println(e.Translate(trans))
					}
				}
				return fmt.Errorf("failed to parse config: %w", err)
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
			clientConfig.QPS = float32(cfg.K8sClientRateLimiterQPS)
			clientConfig.Burst = cfg.K8sClientRateLimiterBurst

			// Default to Protobuf encoding for API responses, support fallback to JSON
			clientConfig.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
			clientConfig.ContentType = "application/vnd.kubernetes.protobuf"

			k8sClient, err := kubernetes.NewForConfig(clientConfig)
			if err != nil {
				logger.Error("failed to create clientset", zap.Error(err))
			}

			controller.Run(ctx, logger, k8sClient, cfg)

			return nil
		},
	}

	AddConfigFlags(cmd)
	cmd.AddCommand(linter.New())
	cmd.AddCommand(version.New())
	if err := en_translations.RegisterDefaultTranslations(validate, trans); err != nil {
		log.Fatalf("failed to register translations: %v", err)
	}

	return cmd
}
