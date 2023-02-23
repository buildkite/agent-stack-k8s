package main

import (
	"log"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/controller"
)

func main() {
	if err := controller.New().Execute(); err != nil {
		log.Fatal(err)
	}
}
