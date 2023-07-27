package integration_test

import (
	"embed"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/cmd/controller"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
)

const (
	repoHTTP = "https://github.com/buildkite/agent-stack-k8s"
	repoSSH  = "git@github.com:buildkite/agent-stack-k8s"
)

var (
	branch                  string
	preservePipelines       bool
	deleteOrphanedPipelines bool
	cfg                     config.Config

	//go:embed fixtures/*
	fixtures embed.FS
)

func TestMain(m *testing.M) {
	var err error

	cmd := controller.New()
	cmd.Flags().BoolVar(
		&preservePipelines,
		"preserve-pipelines",
		false,
		"preserve pipelines created by tests",
	)
	cmd.Flags().BoolVar(
		&deleteOrphanedPipelines,
		"delete-orphaned-pipelines",
		false,
		"delete all pipelines matching agent-k8s-*",
	)

	if err = os.Chdir(".."); err != nil {
		log.Fatal(err)
	}
	if cfg, err = controller.ParseConfig(cmd, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
	if err = os.Chdir("integration"); err != nil {
		log.Fatal(err)
	}

	if !deleteOrphanedPipelines && branch == "" {
		log.Printf("%t", deleteOrphanedPipelines)
		log.Fatalf(
			`You need to run the tests with a flag: -ldflags="-X %s.branch=$BRANCH_NAME"`,
			reflect.TypeOf(testcase{}).PkgPath(),
		)
	}

	for i, v := range os.Args {
		if strings.Contains(v, "test") {
			os.Args[i] = v
		} else {
			os.Args[i] = ""
		}
	}

	os.Exit(m.Run())
}
