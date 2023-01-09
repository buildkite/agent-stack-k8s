package main

import (
	_ "net/http/pprof"

	"github.com/buildkite/agent-stack-k8s/cmd/controller"
)

func main() {
	controller.New().Execute()
}
