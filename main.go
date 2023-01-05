package main

import (
	"log"

	"github.com/buildkite/agent-stack-k8s/cmd/controller"
	"k8s.io/client-go/kubernetes"

	restconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
	ctx := signals.SetupSignalHandler()

	clientConfig := restconfig.GetConfigOrDie()
	k8sClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		log.Fatalf("failed to create clientset: %v", err)
	}
	cfg, err := controller.ParseConfig()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}
	controller.Run(ctx, k8sClient, cfg)
}
