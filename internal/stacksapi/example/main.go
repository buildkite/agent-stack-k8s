package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/buildkite/agent-stack-k8s/v2/internal/stacksapi"
)

func main() {
	clusterToken := os.Getenv("BUILDKITE_CLUSTER_TOKEN")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client, err := stacksapi.NewClient(clusterToken,
		stacksapi.WithLogger(logger),
		stacksapi.LogHTTPPayloads(),
	)
	if err != nil {
		logger.Error("Failed to create Stacks API client", "error", err)
		os.Exit(1)
	}

	stack, err := client.RegisterStack(context.Background(), stacksapi.RegisterStackRequest{
		Key:      "example-stack",
		Type:     stacksapi.StackTypeCustom,
		QueueKey: stacksapi.DefaultQueue,
		Metadata: map[string]string{
			"test": "true",
		},
	})
	if err != nil {
		logger.Error("Failed to register stack", "error", err)
		os.Exit(1)
	}

	logger.Info("registered stack", "stack", stack)
}
