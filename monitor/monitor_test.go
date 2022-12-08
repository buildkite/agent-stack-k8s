package monitor

import (
	"context"
	"os"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInvalidPipeline(t *testing.T) {
	client := api.NewClient(os.Getenv("BUILDKITE_TOKEN"))
	m, err := New(context.Background(), zap.Must(zap.NewDevelopment()), Config{
		Client:      client,
		MaxInFlight: 1,
		Org:         "foo",
		Pipeline:    "bar",
	})
	require.NoError(t, err)
	job := <-m.Scheduled()
	require.ErrorContains(t, job.Err, "invalid pipeline")
}
