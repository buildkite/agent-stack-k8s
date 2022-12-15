package monitor

import (
	"context"
	"os"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func TestSynchronize(t *testing.T) {
	tag := "some-tag=yep"
	ctx := context.Background()
	jobs := []runtime.Object{
		&batchv1.Job{
			ObjectMeta: v1.ObjectMeta{
				Name: "not-our-job",
			},
		},
		&batchv1.Job{
			ObjectMeta: v1.ObjectMeta{
				Name: "different-tag",
				Labels: map[string]string{
					api.TagLabel:  "something-else",
					api.UUIDLabel: "1",
				},
			},
		},
		&batchv1.Job{
			ObjectMeta: v1.ObjectMeta{
				Name: "matching-tag",
				Labels: map[string]string{
					api.TagLabel:  TagToLabel(tag),
					api.UUIDLabel: "2",
				},
			},
		},
	}
	client := fake.NewSimpleClientset(jobs...)
	m, err := New(ctx, zap.Must(zap.NewDevelopment()), client, Config{
		Org:  "foo",
		Tags: []string{tag},
	})
	require.NoError(t, err)
	require.Equal(t, 1, m.knownBuilds.Len())

}
