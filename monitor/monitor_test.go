package monitor

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInvalidPipeline(t *testing.T) {
	m, err := New(context.Background(), zap.Must(zap.NewDevelopment()), Config{
		Token:       os.Getenv("BUILDKITE_TOKEN"),
		MaxInFlight: 1,
		Org:         "foo",
		Pipeline:    "bar",
	})
	require.NoError(t, err)
	job := <-m.Scheduled()
	require.ErrorContains(t, job.Err, "invalid pipeline")
}
