package controller

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/linter"
	"github.com/buildkite/agent-stack-k8s/v2/cmd/version"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var configFile string

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
	c.SquashTagOption = "inline"
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
		if err := processPodSpecPatch(cfg.PodSpecPatch); err != nil {
			return nil, fmt.Errorf("invalid pod spec patch: %w", err)
		}
	}

	for name, rc := range cfg.ResourceClasses {
		err := recapitalizeHugePagesResourceClasses(rc)
		if err != nil {
			return nil, fmt.Errorf("invalid resource class %q: %w", name, err)
		}
	}

	if _, err := resource.ParseQuantity(cfg.ImageCheckContainerCPULimit); err != nil {
		return nil, fmt.Errorf("invalid CPU resource limit defined: %s", cfg.ImageCheckContainerCPULimit)
	}

	if _, err := resource.ParseQuantity(cfg.ImageCheckContainerMemoryLimit); err != nil {
		return nil, fmt.Errorf("invalid memory resource limit defined: %s", cfg.ImageCheckContainerMemoryLimit)
	}

	return cfg, nil
}

func processPodSpecPatch(podSpec *corev1.PodSpec) error {
	for idx, c := range podSpec.Containers {
		if c.Image != strings.ToLower(c.Image) {
			return fmt.Errorf("container image for container at index %d contains uppercase letters: %s", idx, c.Image)
		}

		if len(c.Command) != 0 || len(c.Args) != 0 {
			return fmt.Errorf("container at index %d (image: %s): %w", idx, c.Image, scheduler.ErrNoCommandModification)
		}

		var err error
		if podSpec.Containers[idx].Resources.Requests, err = recapitalizeHugePagesResourceMap(c.Resources.Requests); err != nil {
			return fmt.Errorf("processing resource requests for container at index %d (image: %s): %w", idx, c.Image, err)
		}

		if podSpec.Containers[idx].Resources.Limits, err = recapitalizeHugePagesResourceMap(c.Resources.Limits); err != nil {
			return fmt.Errorf("processing resource limits for container at index %d (image: %s): %w", idx, c.Image, err)
		}
	}

	return nil
}

// Viper downcases keys in maps used in inputs, but in the case of resource classes where hugepages are used, it
// gets downcased to "hugepages-2mi". This function fixes that by recapitlizing the "Mi" part for any hugepages resource
// claims. This is important because the hugepages resource name must be capitalized as "hugepages-2Mi" (or similar),
// otherwise k8s won't recognize the resource claim.
func recapitalizeHugePagesResourceClasses(rc *config.ResourceClass) error {
	var err error
	if rc.Resource.Limits, err = recapitalizeHugePagesResourceMap(rc.Resource.Limits); err != nil {
		return fmt.Errorf("invalid resource class limits: %w", err)
	}

	rc.Resource.Requests, err = recapitalizeHugePagesResourceMap(rc.Resource.Requests)
	if err != nil {
		return fmt.Errorf("invalid resource class requests: %w", err)
	}

	return nil
}

var hugepagesSizeRE = regexp.MustCompile(`^(\d+)([KMGPEkmgpe])i$`)

func recapitalizeHugePagesResourceMap(m corev1.ResourceList) (corev1.ResourceList, error) {
	if m == nil {
		return nil, nil
	}

	newResourceList := corev1.ResourceList{}
	for res, qty := range m {
		stringRes := string(res)
		if !strings.HasPrefix(stringRes, corev1.ResourceHugePagesPrefix) {
			newResourceList[res] = qty
			continue
		}

		hugepagesSize := strings.TrimPrefix(stringRes, corev1.ResourceHugePagesPrefix)

		matched := hugepagesSizeRE.FindStringSubmatch(hugepagesSize)
		if len(matched) != 3 {
			return nil, fmt.Errorf("couldn't match hugepages size %q to regex %s. This is a bug, please contact support@buildkite.com", hugepagesSize, hugepagesSizeRE)
		}

		magnitude := matched[1]
		unitFirstLetter := matched[2]

		correctedSize := magnitude + strings.ToUpper(unitFirstLetter) + "i"
		correctedResourceName := corev1.ResourceName(corev1.ResourceHugePagesPrefix + correctedSize)

		newResourceList[correctedResourceName] = qty
	}

	return newResourceList, nil
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

			level := slog.LevelInfo
			if cfg.LogLevel != "" {
				err := level.UnmarshalText([]byte(cfg.LogLevel))
				if err != nil {
					return fmt.Errorf("invalid log level %q: %w", cfg.LogLevel, err)
				}
			}

			if cfg.Debug {
				// The debug flag trumps the log level flag
				level = slog.LevelDebug
			}

			var handler slog.Handler
			switch cfg.LogFormat {
			case "json":
				handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})

			case "logfmt":
				handler = tint.NewHandler(os.Stdout, &tint.Options{
					AddSource: true,
					Level:     level,
					NoColor:   cfg.NoColor || !isatty.IsTerminal(os.Stdout.Fd()),
				})

			default:
				// Handled by the config validator above, but nice to have just in case.
				return fmt.Errorf("unknown log format: %s", cfg.LogFormat)
			}

			logger := slog.New(handler)
			logger.Info("configuration loaded", "config", cfg)

			clientConfig := restconfig.GetConfigOrDie()
			clientConfig.QPS = float32(cfg.K8sClientRateLimiterQPS)
			clientConfig.Burst = cfg.K8sClientRateLimiterBurst

			// Default to Protobuf encoding for API responses, support fallback to JSON
			clientConfig.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
			clientConfig.ContentType = "application/vnd.kubernetes.protobuf"

			k8sClient, err := kubernetes.NewForConfig(clientConfig)
			if err != nil {
				logger.Error("failed to create clientset", "error", err)
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
