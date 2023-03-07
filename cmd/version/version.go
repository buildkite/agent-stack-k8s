package version

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/buildkite/agent-stack-k8s/v2/internal/version"
	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Prints the version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Version(cmd.Context(), os.Stdout)
		},
	}
}

func Version(ctx context.Context, out io.WriteCloser) error {
	fmt.Fprintf(out, "%s\n", version.Version())
	return nil
}
