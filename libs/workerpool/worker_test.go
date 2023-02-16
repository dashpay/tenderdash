package workerpool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmrequire "github.com/tendermint/tendermint/internal/test/require"
)

func TestWorkerPool(t *testing.T) {
	generateJobs := func(n int) []Job {
		jobs := make([]Job, n)
		for i := 0; i < n; i++ {
			jobs[i] = &valueJob{value: i}
		}
		return jobs
	}
	testCases := []struct {
		poolSize        int
		jobs            []Job
		wantConsumeJobs int
		doneCounter     int32
		wantProducerErr string
		wantConsumerErr string
	}{
		{
			poolSize:        2,
			jobs:            generateJobs(10),
			wantConsumeJobs: 3,
			doneCounter:     1,
			wantProducerErr: ErrWorkerPoolStopped.Error(),
		},
		{
			poolSize:        2,
			jobs:            generateJobs(3),
			wantConsumeJobs: 10,
			doneCounter:     1,
			wantConsumerErr: ErrWorkerPoolStopped.Error(),
		},
		{
			poolSize:        2,
			jobs:            generateJobs(10),
			wantConsumeJobs: 10,
			doneCounter:     2,
		},
	}
	for i, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wp := New(tc.poolSize)
			wp.Run(ctx)
			var counter atomic.Int32
			go func() {
				defer counter.Add(1)
				err := wp.Add(ctx, tc.jobs...)
				tmrequire.Error(t, tc.wantProducerErr, err)
			}()
			go func() {
				defer counter.Add(1)
				_, err := consumeResult(ctx, wp, tc.wantConsumeJobs)
				tmrequire.Error(t, tc.wantConsumerErr, err)
			}()
			require.Eventually(t, func() bool {
				return counter.Load() == tc.doneCounter
			}, 100*time.Millisecond, 10*time.Millisecond)
			wp.Stop(ctx)
		})
	}
}

func consumeResult(ctx context.Context, wp *WorkerPool, num int) ([]Result, error) {
	var err error
	results := make([]Result, num)
	for j := 0; j < num; j++ {
		results[j], err = wp.Receive(ctx)
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

type valueJob struct {
	value int
}

func (j *valueJob) Execute(ctx context.Context) Result {
	return Result{
		Value: j.value,
	}
}
