package abciclient

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSocketClientCheckTxAfterPrepareProposal tests that the socket client
// executes CheckTx after PrepareProposal due to use of priority queue.
//
// Given the socket client and the socket server and socket client waiting
// When the socket client sends a CheckTx request followed by a PrepareProposal request
// Then the socket server executes the PrepareProposal request before the CheckTx request
func TestCheckTxAfterPrepareProposal(t *testing.T) {
	// how long we sleep between each request
	const SleepTime = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	logger := log.NewTestingLogger(t)
	socket := "unix://" + t.TempDir() + "/socket"

	app := &testApp{
		t:      t,
		logger: logger,
	}

	server, err := server.NewServer(logger, socket, "socket", app)
	require.NoError(t, err)
	err = server.Start(ctx)
	require.NoError(t, err)

	cli1 := NewSocketClient(logger, socket, true, nil)
	cli := cli1.(*socketClient)
	cli.setBlocking(true)

	err = cli.Start(ctx)
	require.NoError(t, err)

	// wait until all requests are queued; we're unlock this when we're ready to start the test, and then
	// lock inside Info handler will be unlocked
	app.wait.Lock()

	// Send Info request that will block on app.wait mutex.
	// Then send checkTx followed by PreparaProposal,
	// but the server should execute PrepareProposal first due to use of priority queue
	requests := []types.Request{
		{Value: &types.Request_Info{}},
		{Value: &types.Request_CheckTx{}},
		{Value: &types.Request_PrepareProposal{}},
	}

	wg := sync.WaitGroup{}
	for i, req := range requests {
		wg.Add(1)
		// We use goroutines because these functions are blocking
		go func(i int, req types.Request) {
			var err error
			defer wg.Done()
			logger.Info("sending request", "id", i, "req", req.Value)
			switch req.Value.(type) {
			case *types.Request_Info:
				_, err = cli.Info(ctx, &types.RequestInfo{})
			case *types.Request_CheckTx:
				_, err = cli.CheckTx(ctx, &types.RequestCheckTx{})
			case *types.Request_PrepareProposal:
				_, err = cli.PrepareProposal(ctx, &types.RequestPrepareProposal{})
			default:
				t.Logf("unknown request type: %T", req.Value)
			}
			assert.NoError(t, err)
		}(i, req)
		// time to start the goroutine
		time.Sleep(SleepTime)
	}

	// start the test
	logger.Info("UNLOCK AND START THE TEST")
	app.wait.Unlock()

	// wait for the server to finish executing all requests
	wg.Wait()
}

type testApp struct {
	// we will wait on this mutex in Info function until we are ready to start the test
	wait sync.Mutex
	types.BaseApplication
	prepareProposalExecuted bool
	t                       *testing.T
	logger                  log.Logger
}

// Info waits on the mutex until we are ready to start the test
func (app *testApp) Info(_ context.Context, _ *types.RequestInfo) (*types.ResponseInfo, error) {
	app.wait.Lock()
	defer app.wait.Unlock()
	app.logger.Info("Info")
	return &types.ResponseInfo{}, nil
}

func (app *testApp) CheckTx(_ context.Context, _ *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	app.wait.Lock()
	defer app.wait.Unlock()
	app.logger.Info("CheckTx")

	assert.True(app.t, app.prepareProposalExecuted)
	return &types.ResponseCheckTx{Code: types.CodeTypeOK}, nil
}

func (app *testApp) PrepareProposal(_ context.Context, _ *types.RequestPrepareProposal) (*types.ResponsePrepareProposal, error) {
	app.wait.Lock()
	defer app.wait.Unlock()
	app.logger.Info("PrepareProposal")

	assert.False(app.t, app.prepareProposalExecuted)
	app.prepareProposalExecuted = true
	return &types.ResponsePrepareProposal{}, nil
}
