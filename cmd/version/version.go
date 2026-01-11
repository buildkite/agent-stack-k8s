package version

import (
	"fmt"
	"os"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
)

// Run prints the version to stdout.
func Run() error {
	fmt.Fprintf(os.Stdout, "%s\n", version.Version())
	return nil
}
