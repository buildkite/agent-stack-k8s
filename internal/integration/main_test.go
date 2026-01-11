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
	// Build args for config parsing from os.Args
	// Accepts -f, --config, -f=, --config= flags
	var configArgs []string
	for i, arg := range os.Args {
		if arg == "-f" || arg == "--config" {
			if i+1 < len(os.Args) {
				configArgs = append(configArgs, "--config="+os.Args[i+1])
			}
		}
		if strings.HasPrefix(arg, "-f=") {
			configArgs = append(configArgs, "--config="+strings.TrimPrefix(arg, "-f="))
		}
		if strings.HasPrefix(arg, "--config=") {
			configArgs = append(configArgs, arg)
		}
	}

	testCfg, err := controller.BuildConfigFromArgs(configArgs)
	if err != nil {
		log.Fatalf("Error parsing config: %s", err)
	}

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
