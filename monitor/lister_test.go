package monitor

import (
	"context"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestJobLister(t *testing.T) {
	tag := "some-tag=yep"
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
	lister := NewJobListerOrDie(context.Background(), client, tag)

	jobList, err := lister.List(labels.Everything())
	require.NoError(t, err)
	require.Equal(t, 1, len(jobList))
}
