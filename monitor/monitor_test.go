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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestInvalidOrg(t *testing.T) {
	m := New(context.Background(), zap.Must(zap.NewDevelopment()), api.NewBuildkiteJobManagerOrDie(context.Background(), fake.NewSimpleClientset()), Config{
		Token:       os.Getenv("BUILDKITE_TOKEN"),
		MaxInFlight: 1,
		Org:         "foo",
	})
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
					api.TagLabel:  api.TagToLabel(tag),
					api.UUIDLabel: "2",
				},
			},
		},
	}
	client := fake.NewSimpleClientset(jobs...)
	m := New(ctx, zap.Must(zap.NewDevelopment()), api.NewBuildkiteJobManagerOrDie(context.Background(), client, tag), Config{
		Org: "foo",
	})
	jobList, err := m.k8s.JobLister.List(labels.Everything())
	require.NoError(t, err)
	require.Equal(t, 1, len(jobList))
}
