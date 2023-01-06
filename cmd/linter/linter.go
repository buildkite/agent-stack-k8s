package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/buildkite/agent-stack-k8s/scheduler"
	"github.com/buildkite/go-buildkite/v3/buildkite"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

type Options struct {
	File string
}

func (o *Options) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.File, "file", "f", "", "path to the pipeline file, or {-} for stdin")
}

func New() *cobra.Command {
	o := &Options{}

	cmd := &cobra.Command{
		Use:          "lint",
		Short:        "A tool for linting Buildkite pipelines using the agent-stack-k8s plugin",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Lint(cmd.Context(), o)
		},
	}
	o.AddFlags(cmd)

	return cmd
}

func Lint(ctx context.Context, options *Options) error {
	contents, err := os.ReadFile(options.File)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	var pipeline buildkite.Pipeline
	if err := yaml.Unmarshal(contents, &pipeline); err != nil {
		return fmt.Errorf("failed to read pipeline: %w", err)
	}
	for _, step := range pipeline.Steps {
		for name, plugin := range step.Plugins {
			if name == "kubernetes" {
				asJson, err := json.Marshal(plugin)
				if err != nil {
					return fmt.Errorf("failed to marshal plugin to json: %w", err)
				}
				var pluginConfig scheduler.PluginConfig
				decoder := json.NewDecoder(bytes.NewReader(asJson))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&pluginConfig); err != nil {
					return fmt.Errorf("failed to unmarshal Kubernetes plugin: %w", err)
				}
			}
		}
	}

	log.Println("valid!")
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if err := New().Execute(); err != nil {
		log.Fatal(err)
	}
}
