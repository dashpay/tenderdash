package abciclient_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abciserver "github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

// TestGRPCClientServerParallel tests that gRPC client and server can handle multiple parallel requests
func TestGRPCClientServerParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	logger := log.NewNopLogger()
	app := &mockApplication{t: t}

	socket := t.TempDir() + "/grpc_test"
	client, _, err := makeGRPCClientServer(ctx, t, logger, app, socket)
	if err != nil {
		t.Fatal(err)
	}

	// we'll use that mutex to ensure threads don't finish before we check status
	app.mtx.Lock()

	const threads = 5
	// started will be marked as done as soon as app.Info() handler executes on the server
	app.started.Add(threads)
	// done will be used to wait for all threads to finish
	var done sync.WaitGroup
	done.Add(threads)

	for i := 0; i < threads; i++ {
		thread := uint64(i)
		go func() {
			_, _ = client.Info(ctx, &types.RequestInfo{BlockVersion: thread})
			done.Done()
		}()
	}

	// wait for threads to execute
	// note it doesn't mean threads are really done, as they are waiting on the mtx
	// so if all `started` are marked as done, it means all threads have started
	// in parallel
	app.started.Wait()

	// unlock the mutex so that threads can finish their execution
	app.mtx.Unlock()

	// wait for all threads to really finish
	done.Wait()
}

func makeGRPCClientServer(
	ctx context.Context,
	t *testing.T,
	logger log.Logger,
	app types.Application,
	name string,
) (abciclient.Client, service.Service, error) {
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

	client := abciclient.NewGRPCClient(logger.With("module", "abci-client"), socket, true)

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
	// we'll use that to ensure all threads have started
	started sync.WaitGroup

	t *testing.T
}

func (m *mockApplication) Info(_ctx context.Context, req *types.RequestInfo) (res *types.ResponseInfo, err error) {
	m.t.Logf("Info %d called", req.BlockVersion)
	// mark wg as done to signal that we have executed
	m.started.Done()
	// we will wait here until all threads mark wg as done
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.t.Logf("Info %d finished", req.BlockVersion)
	return &types.ResponseInfo{}, nil
}
