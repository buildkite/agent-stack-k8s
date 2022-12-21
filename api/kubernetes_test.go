package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildkiteJobManager(t *testing.T) {
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
					TagLabel:  "something-else",
					UUIDLabel: "1",
				},
			},
		},
		&batchv1.Job{
			ObjectMeta: v1.ObjectMeta{
				Name: "matching-tag",
				Labels: map[string]string{
					TagLabel:  TagToLabel(tag),
					UUIDLabel: "2",
				},
			},
		},
	}
	client := fake.NewSimpleClientset(jobs...)
	jobManager := NewBuildkiteJobManagerOrDie(context.Background(), client, tag)

	jobList, err := jobManager.JobLister.List(labels.Everything())
	require.NoError(t, err)
	require.Equal(t, 1, len(jobList))
}
