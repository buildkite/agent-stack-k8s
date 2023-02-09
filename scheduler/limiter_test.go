package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/buildkite/agent-stack-k8s/api"
	"github.com/buildkite/agent-stack-k8s/monitor"
	"github.com/buildkite/agent-stack-k8s/scheduler"
	gomock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:generate mockgen -destination=mock_handler_test.go -source=../monitor/monitor.go -package scheduler_test

func TestLimiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 1)
	var inFlight int64

	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(5).
		Do(func(ctx context.Context, job *monitor.Job) error {
			currentInFlight := atomic.LoadInt64(&inFlight)
			require.Equal(t, 0, int(currentInFlight))

			// mark job as completed
			go func() {
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
				atomic.AddInt64(&inFlight, -1)
			}()
			return nil
		})

	var wg sync.WaitGroup
	// simulate receiving a bunch of jobs
	for i := 0; i < 5; i++ {
		wg.Add(1)
		job := api.CommandJob{
			Uuid: fmt.Sprintf("job-%d", i),
		}
		go func() {
			require.NoError(t, limiter.Create(ctx, &monitor.Job{
				CommandJob: job,
			}))
			atomic.AddInt64(&inFlight, 1)
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestSkipsDuplicateJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	// no max-in-flight
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 0)

	// only expect 1
	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(1)

	for i := 0; i < 5; i++ {
		limiter.Create(ctx, &monitor.Job{
			CommandJob: api.CommandJob{Uuid: "some-job"},
		})
	}
}

func TestSkipsCreateErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 1)
	invalid := errors.New("invalid")

	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(5).
		DoAndReturn(func(context.Context, *monitor.Job) error {
			return invalid
		})

	for i := 0; i < 5; i++ {
		require.Error(t, invalid, limiter.Create(ctx, &monitor.Job{
			CommandJob: api.CommandJob{Uuid: "some-job"},
		}))
	}
}
