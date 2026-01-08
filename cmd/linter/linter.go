package linter

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/alecthomas/kong"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/xeipuuv/gojsonschema"
	"sigs.k8s.io/yaml"
)

const (
	pipelineSchema = "https://raw.githubusercontent.com/buildkite/pipeline-schema/main/schema.json"
	k8sSchema      = "https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v1.35.0/_definitions.json"
)

//go:embed schema.json
var schema string

// CLI represents the lint subcommand for Kong.
type CLI struct {
	File string `kong:"arg,required,help='Path to the pipeline file'"`
}

// Run parses CLI arguments and executes the lint command.
func Run() error {
	cli := &CLI{}
	parser, err := kong.New(
		cli,
		kong.Name("agent-stack-k8s lint"),
		kong.Description("A tool for linting Buildkite pipelines"),
		kong.UsageOnError(),
	)
	if err != nil {
		return err
	}
	_, err = parser.Parse(os.Args[2:])
	parser.FatalIfErrorf(err)

	return Lint(context.Background(), cli.File)
}

// Lint validates a Buildkite pipeline file against the schema.
func Lint(ctx context.Context, file string) error {
	contents, err := os.ReadFile(file)
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
	compiledSchema, err := schemaLoader.Compile(gojsonschema.NewStringLoader(schema))
	if err != nil {
		return fmt.Errorf("failed to compile schemas: %w", err)
	}
	bs, err := json.Marshal(pipeline)
	if err != nil {
		return fmt.Errorf("failed to marshal pipeline: %w", err)
	}
	documentLoader := gojsonschema.NewBytesLoader(bs)

	result, err := compiledSchema.Validate(documentLoader)
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
