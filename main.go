package main

import (
	"github.com/buildkite/agent-stack-k8s/cmd/controller"
)

func main() {
	controller.New().Execute()
}
