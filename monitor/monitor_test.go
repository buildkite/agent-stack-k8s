package monitor

import (
	"context"
	"os"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInvalidOrg(t *testing.T) {
	tags := []string{"foo=bar"}
	m := New(context.Background(), zap.Must(zap.NewDevelopment()), api.NewJobListerOrDie(context.Background(), fake.NewSimpleClientset(), tags...), Config{
		Token:       os.Getenv("BUILDKITE_TOKEN"),
		MaxInFlight: 1,
		Org:         "foo",
		Tags:        tags,
	})
	job := <-m.Scheduled()
	require.ErrorContains(t, job.Err, "invalid organization")
}

func TestScheduleBuild(t *testing.T) {
	tag := "testtag=matching"
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
					api.TagLabel:  api.TagToLabel(tag),
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

	m := New(context.Background(), zap.Must(zap.NewDevelopment()), api.NewJobListerOrDie(context.Background(), client, tag), Config{
		Token:       "test_token",
		MaxInFlight: 1,
		Org:         "foo",
	})
	tests := []struct {
		name           string
		job            *api.JobJobTypeCommand
		tag            string
		shouldSchedule bool
		shouldError    bool
	}{{
		name: "Matching UUID don't run",
		job: &api.JobJobTypeCommand{
			CommandJob: api.CommandJob{
				Uuid: "2",
			},
		},
		tag:            tag,
		shouldSchedule: false,
		shouldError:    false,
	}, {
		name: "New job should schedule",
		job: &api.JobJobTypeCommand{
			CommandJob: api.CommandJob{
				Uuid: "6",
			},
		},
		tag:            tag,
		shouldSchedule: true,
		shouldError:    false,
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var errResult error
			jobCreated := 0
			functionReturned := 0

			returned := make(chan error)
			go func() {
				err := m.scheduleBuild(test.job, test.tag)
				returned <- err
			}()
			select {
			case <-m.jobs:
				jobCreated++
			case errResult = <-returned:
				functionReturned++
			}
			if test.shouldSchedule {
				if jobCreated == 0 {
					if errResult != nil {
						t.Fatalf("Test should schedule job, but error was returned: %s", errResult)
					} else {
						t.Fatalf("Test should schedule job, but nil error was returned")
					}
				}
			}
			if test.shouldError {
				if errResult == nil {
					t.Fatalf("Test should error, but no error was returned")
				}
			}
		})
	}

}
