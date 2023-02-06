package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	gomock "github.com/golang/mock/gomock"
	"go.uber.org/zap/zaptest"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:generate mockgen -destination=mock_handler_test.go -source=scheduler.go -package scheduler_test

func TestLimiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan monitor.Job)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), input, handler, 1)

	go limiter.Run(ctx)

	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(5)

	// simulate receiving a bunch of jobs
	for i := 0; i < 5; i++ {
		job := api.CommandJob{
			Uuid: fmt.Sprintf("job-%d", i),
		}
		// chan can receive
		input <- monitor.Job{
			CommandJob: job,
		}
		select {
		case input <- monitor.Job{CommandJob: job}:
			t.Error("channel should not receive, max in flight should be reached")
		default:
			t.Log("max-in-flight reached")
		}

		// mark job as completed
		limiter.OnUpdate(nil, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					api.UUIDLabel: job.Uuid,
				},
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobComplete},
				},
			},
		})
	}
}

func TestSkipsDuplicateJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan monitor.Job)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	// no max-in-flight
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), input, handler, 0)

	go limiter.Run(ctx)

	// only expect 1
	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(1)

	for i := 0; i < 5; i++ {
		job := api.CommandJob{
			Uuid: "job-0",
		}
		// chan can receive
		input <- monitor.Job{
			CommandJob: job,
		}
	}
}

func TestSkipsCreateErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan monitor.Job)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), input, handler, 1)

	go limiter.Run(ctx)

	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(5).
		DoAndReturn(func(context.Context, *monitor.Job) error {
			return errors.New("invalid")
		})

	for i := 0; i < 5; i++ {
		job := api.CommandJob{
			Uuid: "job-0",
		}
		// chan can receive
		input <- monitor.Job{
			CommandJob: job,
		}
	}
}
