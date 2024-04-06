package linter

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/cobra"
	"github.com/xeipuuv/gojsonschema"
	"sigs.k8s.io/yaml"
)

const (
	pipelineSchema = "https://raw.githubusercontent.com/buildkite/pipeline-schema/main/schema.json"
	k8sSchema      = "https://kubernetesjsonschema.dev/master/_definitions.json"
)

/go:embed schema.json
var schema string

type Options struct {
	File string `validate:"required,file"`
}

func (o *Options) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.File, "file", "f", "", "path to the pipeline file, or {-} for stdin")
}

func (o *Options) Validate() error {
	return validator.New().Struct(o)
}

func New() *cobra.Command {
	o := &Options{}

	cmd := &cobra.Command{
		Use:          "lint",
		Short:        "A tool for linting Buildkite pipelines",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.Validate(); err != nil {
				return fmt.Errorf("failed to validate config: %w", err)
			}
			return Lint(cmd.Context(), o)
		},
	}
	o.AddFlags(cmd)

	return cmd
}

func Lint(ctx context.Context, options *Options) error {
	contents, err := os.ReadFile(options.File)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	var pipeline buildkite.Pipeline
	if err := yaml.Unmarshal(contents, &pipeline); err != nil {
		return fmt.Errorf("failed to unmarshal pipeline: %w", err)
	}
	for _, step := range pipeline.Steps {
		for name, plugin := range step.Plugins {
			if name == "kubernetes" {
				asJson, err := json.Marshal(plugin)
				if err != nil {
					return fmt.Errorf("failed to marshal plugin to json: %w", err)
				}
				var pluginConfig scheduler.KubernetesPlugin
				decoder := json.NewDecoder(bytes.NewReader(asJson))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&pluginConfig); err != nil {
					return fmt.Errorf("failed to unmarshal Kubernetes plugin: %w", err)
				}
				for i, container := range pluginConfig.PodSpec.Containers {
					if container.Name == "" {
						container.Name = fmt.Sprintf("container-%d", i)
					}
				}
				bs, err := json.Marshal(pluginConfig)
				if err != nil {
					return fmt.Errorf("failed to remarshal Kubernetes plugin: %w", err)
				}
				var plugin buildkite.Plugin
				if err := json.Unmarshal(bs, &plugin); err != nil {
					return fmt.Errorf(
						"failed to unmarshal Kubernetes plugin back to buildkite plugin: %w",
						err,
					)
				}
				step.Plugins[name] = plugin
			}
		}
	}
	schemaLoader := gojsonschema.NewSchemaLoader()
	if err := schemaLoader.AddSchema(pipelineSchema, gojsonschema.NewReferenceLoader(pipelineSchema)); err != nil {
		return fmt.Errorf("failed to add pipeline schema: %w", err)
	}
	if err := schemaLoader.AddSchema(k8sSchema, gojsonschema.NewReferenceLoader(k8sSchema)); err != nil {
		return fmt.Errorf("failed to add kubernetes schema: %w", err)
	}
	schema, err := schemaLoader.Compile(gojsonschema.NewStringLoader(schema))
	if err != nil {
		return fmt.Errorf("failed to compile schemas: %w", err)
	}
	bs, err := json.Marshal(pipeline)
	if err != nil {
		return fmt.Errorf("failed to marshal pipeline: %w", err)
	}
	documentLoader := gojsonschema.NewBytesLoader(bs)

	result, err := schema.Validate(documentLoader)
	if err != nil {
		return fmt.Errorf("failed to validate: %w", err)
	}

	if result.Valid() {
		log.Println("The document is valid")
	} else {
		log.Println("The document is not valid. see errors:")
		for _, desc := range result.Errors() {
			log.Printf("- %s", desc)
		}
	}

	return nil
}
