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
	"github.com/spf13/cobra"
)

const (
	repoHTTP = "https://github.com/buildkite/agent-stack-k8s"
	repoSSH  = "git@github.com:buildkite/agent-stack-k8s"
)

var (
	branch           string
	cfg              config.Config
	cleanupPipelines bool
	// Preserve pipelines even if the test passses.
	// By default, failed pipeline will always be kept.
	preservePipelines bool

	//go:embed fixtures/*
	fixtures embed.FS
)

func TestMain(m *testing.M) {
	cmd := &cobra.Command{}
	controller.AddConfigFlags(cmd)
	v, err := controller.ReadConfigFromFileArgsAndEnv(cmd, os.Args[1:])
	if err != nil {
		log.Fatalf("Error reading config: %s", err)
	}

	testCfg, err := controller.ParseAndValidateConfig(v)
	if err != nil {
		log.Fatalf("Error parsing config: %s", err)
	}

	// Disable node scaler for integration tests to prevent background goroutines
	// from interfering with test execution
	testCfg.EnableNodeScaler = false
	
	cfg = *testCfg

	cleanupPipelines = parseBoolEnvVar("CLEANUP_PIPELINES")
	preservePipelines = parseBoolEnvVar("PRESERVE_PIPELINES")

	if !cleanupPipelines && branch == "" {
		log.Fatalf(
			`The tests need to be run with the flag: -ldflags="-X %s.branch=$BRANCH_NAME"`,
			reflect.TypeOf(testcase{}).PkgPath(),
		)
	}

	// remove flags parsed by controller.ParseConfig
	newArgs := []string{}
	for _, v := range os.Args {
		if strings.HasPrefix(v, "-test") {
			newArgs = append(newArgs, v)
		}
	}
	os.Args = newArgs

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
