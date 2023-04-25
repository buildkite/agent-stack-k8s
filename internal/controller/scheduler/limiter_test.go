package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/buildkite/agent-stack-k8s/v2/api"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/config"
	"github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	gomock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:generate go run github.com/golang/mock/mockgen -destination=mock_handler_test.go -source=../monitor/monitor.go -package scheduler_test

func TestLimiter(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 1)

	var wg sync.WaitGroup
	wg.Add(5)
	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(5).
		Do(func(ctx context.Context, job *api.CommandJob) error {
			go func() {
				t.Log("updating", job.Uuid)
				limiter.OnUpdate(nil, &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							config.UUIDLabel: job.Uuid,
						},
					},
					Status: batchv1.JobStatus{
						Conditions: []batchv1.JobCondition{
							{Type: batchv1.JobComplete},
						},
					},
				})
				t.Log("did update for", job.Uuid)
				wg.Done()
			}()
			return nil
		})

	// simulate receiving a bunch of jobs
	wg.Add(5)
	for i := 0; i < 5; i++ {
		job := api.CommandJob{
			Uuid:            fmt.Sprintf("job-%d", i),
			AgentQueryRules: []string{},
		}
		go func() {
			t.Log("creating", job.Uuid)
			require.NoError(t, limiter.Create(ctx, &job))
			require.LessOrEqual(t, limiter.InFlight(), 1)
			t.Log("did create for", job.Uuid)
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestSkipsDuplicateJobs(t *testing.T) {
	t.Parallel()

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
		err := limiter.Create(ctx, &api.CommandJob{
			Uuid:            "some-job",
			AgentQueryRules: []string{},
		})
		assert.NoError(t, err)
	}
}

func TestSkipsCreateErrors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	handler := NewMockJobHandler(ctrl)
	limiter := scheduler.NewLimiter(zaptest.NewLogger(t), handler, 1)
	invalid := errors.New("invalid")

	handler.EXPECT().Create(gomock.Eq(ctx), gomock.Any()).Times(5).
		DoAndReturn(func(context.Context, *api.CommandJob) error {
			return invalid
		})

	for i := 0; i < 5; i++ {
		require.Error(t, invalid, limiter.Create(ctx, &api.CommandJob{
			Uuid:            "some-job",
			AgentQueryRules: []string{},
		}))
	}
}
