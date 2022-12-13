package monitor

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes/fake"
)

func TestInvalidOrg(t *testing.T) {
	m, err := New(context.Background(), zap.Must(zap.NewDevelopment()), fake.NewSimpleClientset(), Config{
		Token:       os.Getenv("BUILDKITE_TOKEN"),
		MaxInFlight: 1,
		Org:         "foo",
		Tags:        []string{"foo"},
	})
	require.NoError(t, err)
	job := <-m.Scheduled()
	require.ErrorContains(t, job.Err, "invalid organization")
}
