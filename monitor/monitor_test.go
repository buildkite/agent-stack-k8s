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
	m, err := New(context.Background(), zap.Must(zap.NewDevelopment()), fake.NewSimpleClientset(), api.Config{
		BuildkiteToken: os.Getenv("BUILDKITE_TOKEN"),
		MaxInFlight:    1,
		Org:            "foo",
		Tags:           []string{"foo=bar"},
	})
	require.NoError(t, err)
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

	m, err := New(context.Background(), zap.Must(zap.NewDevelopment()), client, api.Config{
		MaxInFlight: 1,
		Org:         "foo",
		Tags:        []string{tag},
	})
	require.NoError(t, err)
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
	m.jobs = make(chan Job, 1)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := m.scheduleBuild(test.job, test.tag)
			var job *Job
			select {
			case j := <-m.jobs:
				job = &j
			default:
			}
			if test.shouldSchedule {
				if job == nil {
					if err != nil {
						t.Fatalf("Test should schedule job, but error was returned: %s", err)
					} else {
						t.Fatalf("Test should schedule job, but nil error was returned")
					}
				}
			}
			if test.shouldError {
				if err == nil {
					t.Fatalf("Test should error, but no error was returned")
				}
			}
		})
	}
}
