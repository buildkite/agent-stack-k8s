package monitor

import (
	"context"
	"os"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes/fake"
)

func TestInvalidOrg(t *testing.T) {
	m, err := New(zap.Must(zap.NewDevelopment()), fake.NewSimpleClientset(), api.Config{
		BuildkiteToken: os.Getenv("BUILDKITE_TOKEN"),
		MaxInFlight:    1,
		Org:            "foo",
		Tags:           []string{"foo=bar"},
	})
	require.NoError(t, err)

	require.ErrorContains(t, <-m.Start(context.Background(), nil), "invalid organization")
}
