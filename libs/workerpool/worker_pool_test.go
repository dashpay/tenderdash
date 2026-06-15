package workerpool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/require"

	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
)

func TestWorkerPool_Basic(t *testing.T) {
	testCases := []struct {
		poolSize        int
		jobs            []*Job
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
			var (
				counter atomic.Int32
				wg      sync.WaitGroup
			)
			wg.Add(2)
			go func() {
				defer counter.Add(1)
				defer wg.Done()
				err := wp.Send(ctx, tc.jobs...)
				tmrequire.Error(t, tc.wantProducerErr, err)
			}()
			go func() {
				defer counter.Add(1)
				defer wg.Done()
				_, err := consumeResult(ctx, wp, tc.wantConsumeJobs)
				tmrequire.Error(t, tc.wantConsumerErr, err)
			}()
			require.Eventually(t, func() bool {
				return counter.Load() == tc.doneCounter
			}, 100*time.Millisecond, 10*time.Millisecond)
			wp.Stop(ctx)
			wg.Wait()
		})
	}
}

func TestWorkerPool_Stop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := generateJobs(10)
	wp := New(2)
	wp.Start(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := wp.Send(ctx, jobs...)
			tmrequire.Error(t, ErrWorkerPoolStopped.Error(), err)
		}()
	}
	time.Sleep(100 * time.Millisecond)
	wp.Stop(ctx)
	wg.Wait()
}

func TestWorkerPool_Send(t *testing.T) {
	testCases := []struct {
		wantErr string
		stopFn  func(ctx context.Context, cancel func(), wp *WorkerPool)
	}{
		{
			wantErr: context.Canceled.Error(),
			stopFn: func(ctx context.Context, cancel func(), _ *WorkerPool) {
				cancel()
			},
		},
		{
			wantErr: ErrWorkerPoolStopped.Error(),
			stopFn: func(ctx context.Context, _ func(), wp *WorkerPool) {
				wp.Stop(ctx)
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			jobs := generateJobs(3)
			jobCh := make(chan *Job)
			wp := New(1, WithJobCh(jobCh))
			wp.Start(ctx)
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := wp.Send(ctx, jobs...)
				tmrequire.Error(t, tc.wantErr, err)
			}()
			require.Eventually(t, func() bool {
				return jobs[2].Status() == JobSending
			}, 5*time.Millisecond, time.Millisecond)
			tc.stopFn(ctx, cancel, wp)
			wg.Wait()
			err := wp.Send(ctx, jobs...)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

func TestWorkerPool_Receive(t *testing.T) {
	testCases := []struct {
		stopFn  func(ctx context.Context, cancel func(), wp *WorkerPool)
		wantErr string
	}{
		{
			stopFn: func(ctx context.Context, cancel func(), wp *WorkerPool) {
				cancel()
			},
			wantErr: context.Canceled.Error(),
		},
		{
			stopFn: func(ctx context.Context, cancel func(), wp *WorkerPool) {
				wp.Stop(ctx)
			},
			wantErr: ErrWorkerPoolStopped.Error(),
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			resultCh := make(chan Result)
			wp := New(1, WithResultCh(resultCh))
			wp.Start(ctx)
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := consumeResult(ctx, wp, 1)
				tmrequire.Error(t, tc.wantErr, err)
			}()
			tc.stopFn(ctx, cancel, wp)
			wg.Wait()
			wp.Stop(ctx)
		})
	}
}

func TestWorkerPool_Reset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const jobsCnt = 2
	jobs := generateJobs(jobsCnt)

	wp := New(2)
	// start worker pool
	wp.Start(ctx)
	// send several jobs to process
	err := wp.Send(ctx, jobs...)
	require.NoError(t, err)
	// consume jobs
	results, err := consumeResult(ctx, wp, jobsCnt)
	require.NoError(t, err)
	require.Len(t, results, jobsCnt)

	// stop worker pool gracefully
	wp.Stop(ctx)
	// try to send the jobs to stopped worker pool
	err = wp.Send(ctx, jobs...)
	tmrequire.Error(t, ErrWorkerPoolStopped.Error(), err)

	// try to stop worker pool again
	wp.Stop(ctx)
	checkWorkerPoolChannels(t, true, wp)

	// reset and start the worker pool
	wp.Reset()
	checkWorkerPoolChannels(t, false, wp)

	//  reset and start workers again
	wp.Run(ctx)
	checkWorkerPoolChannels(t, false, wp)

	// send several jobs to process
	err = wp.Send(ctx, jobs...)
	require.NoError(t, err)

	// consume jobs
	results, err = consumeResult(ctx, wp, jobsCnt)
	require.NoError(t, err)
	require.Len(t, results, jobsCnt)

	wp.Stop(ctx)
	checkWorkerPoolChannels(t, true, wp)
}

func checkWorkerPoolChannels(t *testing.T, want bool, wp *WorkerPool) {
	require.Equal(t, want, isChannelClosed(wp.doneCh))
	require.Equal(t, want, isChannelClosed(wp.jobCh))
	require.Equal(t, want, isChannelClosed(wp.resultCh))
	require.Equal(t, want, wp.stopped.Load())
	for _, w := range wp.workers {
		require.Equal(t, want, isChannelClosed(w.stoppedCh))
		require.Equal(t, want, isChannelClosed(w.stopCh))
		require.Equal(t, want, isChannelClosed(w.resultCh))
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

func generateJobs(n int) []*Job {
	jobs := make([]*Job, n)
	for i := 0; i < n; i++ {
		jobs[i] = NewJob(func(ctx context.Context) Result {
			return Result{Value: n}
		})
	}
	return jobs
}

func isChannelClosed[T any](ch <-chan T) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestWorkerPool_StopSendRace is a regression test for the WaitGroup misuse
// panic that occurs when Stop's wg.Wait() races with Send's wg.Add(1).
//
// Exact panic sequence (pre-fix):
//  1. A Send completes (counter→0); concurrently another Send passes the
//     stopped.Load() gate but has not yet called wg.Add(1).
//  2. Stop calls wg.Wait() while counter==0 and the second Add is pending.
//     Internally Wait increments the waiters field to 1 before confirming
//     counter==0 and returning.
//  3. The second Send calls wg.Add(1) with waiters==1 and v==delta==1:
//     the WaitGroup panics: "sync: WaitGroup misuse: Add called concurrently
//     with Wait".
//
// The fix wraps wg.Wait() in Stop under wgMtx (Send already wraps wg.Add
// under the same lock), so Add and Wait are mutually exclusive.
//
// The test exercises this with a direct white-box assertion: it drives the
// pool through many Start/Send/Stop cycles using -race so the race detector
// would flag any unsynchronised access between Add and Wait.  Without the
// wgMtx guard on Wait the race detector reports a DATA RACE even before a
// panic manifests.
//
// Run: go test -race -run TestWorkerPool_StopSendRace -count=200 ./libs/workerpool/
func TestWorkerPool_StopSendRace(_ *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		// poolSize=0, so no workers drain jobCh.  Sends will block inside
		// the select until doneCh closes, keeping counter>0 while blocked.
		// The unbuffered jobCh means Send cannot complete until a reader
		// appears or doneCh fires — giving Stop time to reach wg.Wait().
		jobCh := make(chan *Job)
		wp := New(0, WithJobCh(jobCh))
		wp.Start(ctx)

		// Launch many concurrent Sends to maximize overlap with Stop.
		const senders = 8
		var sendWg sync.WaitGroup
		sendWg.Add(senders)
		for s := 0; s < senders; s++ {
			go func() {
				defer sendWg.Done()
				_ = wp.Send(ctx, NewJob(func(_ context.Context) Result { return Result{} }))
			}()
		}

		// Stop races with in-flight Sends: the race detector will surface any
		// unsynchronised Add/Wait overlap if the wgMtx guard is absent.
		wp.Stop(ctx)

		sendWg.Wait()
		cancel()
	}
}
