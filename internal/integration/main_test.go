package integration_test

import (
	"embed"
	"log"
	"os"
	"reflect"
	"strconv"
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
	branch            string
	cfg               config.Config
	cleanupPipelines  bool
	preservePipelines bool

	//go:embed fixtures/*
	fixtures embed.FS
)

func TestMain(m *testing.M) {
	var err error

	cmd := controller.New()
	if err = os.Chdir("../.."); err != nil {
		log.Fatalf("Error changing dir: %s", err)
	}
	if cfg, err = controller.ParseConfig(cmd, os.Args[1:]); err != nil {
		log.Fatalf("Error parsing config: %s", err)
	}
	if err = os.Chdir("internal/integration"); err != nil {
		log.Fatalf("Error changing dir: %s", err)
	}

	cleanupPipelines = parseBoolEnvVar("CLEANUP_PIPELINES")
	preservePipelines = parseBoolEnvVar("PRESERVE_PIPELINES")

	if !cleanupPipelines && branch == "" {
		log.Fatalf(
			`The tests need to be run with the flag: -ldflags="-X %s.branch=$BRANCH_NAME"`,
			reflect.TypeOf(testcase{}).PkgPath(),
		)
	}

	// remove flags parsed by controller.ParseConfig
	for i, v := range os.Args {
		if strings.Contains(v, "test") {
			os.Args[i] = v
		} else {
			os.Args[i] = ""
		}
	}

	os.Exit(m.Run())
}

func parseBoolEnvVar(name string) bool {
	e := os.Getenv(name)
	if e == "" {
		return false
	}
	b, err := strconv.ParseBool(e)
	if err != nil {
		log.Fatalf("Error parsing %s: %s", name, err)
	}
	return b
}
