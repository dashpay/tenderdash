package abciclient

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"

	abciserver "github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

// TestGRPCClientServerParallel tests that gRPC client and server can handle multiple parallel requests
func TestGRPCClientServerParallel(t *testing.T) {
	const (
		timeout = 1 * time.Second
		tick    = 10 * time.Millisecond
	)

	type testCase struct {
		threads     int
		concurrency uint16
	}

	testCases := []testCase{
		{threads: 1, concurrency: 1},
		{threads: 2, concurrency: 1},
		{threads: 2, concurrency: 2},
		{threads: 5, concurrency: 0},
		{threads: 5, concurrency: 1},
		{threads: 5, concurrency: 2},
		{threads: 5, concurrency: 5},
	}

	logger := log.NewNopLogger()

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			app := &mockApplication{t: t, concurrencyLimit: int32(tc.concurrency)}

			socket := t.TempDir() + "/grpc_test"
			client, _, err := makeGRPCClientServer(ctx, t, logger, app, socket, tc.concurrency)
			if err != nil {
				t.Fatal(err)
			}

			// we'll use that mutex to ensure threads don't finish before we check status
			app.mtx.Lock()

			// done will be used to wait for all threads to finish
			var done sync.WaitGroup

			for i := 0; i < tc.threads; i++ {
				done.Add(1)
				thread := uint64(i)
				go func() {
					// we use BlockVersion for logging purposes, so we put thread id there
					_, _ = client.Info(ctx, &types.RequestInfo{BlockVersion: thread})
					done.Done()
				}()
			}

			expectThreads := int32(tc.concurrency)
			if expectThreads == 0 {
				expectThreads = int32(tc.threads)
			}

			// wait for all threads to start execution
			assert.Eventually(t, func() bool {
				return app.running.Load() == expectThreads
			}, timeout, tick, "not all threads started in time")

			// ensure no other threads will start
			time.Sleep(2 * tick)

			// unlock the mutex so that threads can finish their execution
			app.mtx.Unlock()

			// wait for all threads to really finish
			done.Wait()
		})
	}
}

func makeGRPCClientServer(
	ctx context.Context,
	t *testing.T,
	logger log.Logger,
	app types.Application,
	name string,
	limit uint16,
) (Client, service.Service, error) {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	t.Cleanup(leaktest.Check(t))

	// Start the listener
	socket := fmt.Sprintf("unix://%s.sock", name)

	server := abciserver.NewGRPCServer(logger.With("module", "abci-server"), socket, app)

	if err := server.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}

	client := NewGRPCClient(logger.With("module", "abci-client"), socket, true)
	client.(*grpcClient).SetMaxConcurrentStreams(limit)

	if err := client.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}
	return client, server, nil
}

// mockApplication that will decrease mockApplication.started when called Info, and then wait until
// mtx is unlocked before it finishes
type mockApplication struct {
	types.BaseApplication
	mtx sync.Mutex

	running atomic.Int32
	// concurrencyLimit of concurrent requests
	concurrencyLimit int32

	t *testing.T
}

func (m *mockApplication) Info(_ctx context.Context, req *types.RequestInfo) (res *types.ResponseInfo, err error) {
	m.t.Logf("Info %d called", req.BlockVersion)
	running := m.running.Add(1)
	defer m.running.Add(-1)

	if m.concurrencyLimit > 0 {
		assert.LessOrEqual(m.t, running, m.concurrencyLimit, "too many requests running in parallel")
	}

	// we will wait here until all expected threads are running
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.t.Logf("Info %d finished", req.BlockVersion)

	return &types.ResponseInfo{}, nil
}
