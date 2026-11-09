package abciclient_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	clientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	"github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestRouting tests the RoutedClient.
//
// Given 3 clients: defaultApp, consensusApp and queryApp:
// * when a request of type Info is made, it should be routed to defaultApp
// * when a request of type FinalizeBlock is made, it should be first routed to queryApp, then to consensusApp
// * when a request of type CheckTx is made, it should be routed to queryApp
// * when a request of type PrepareProposal is made, it should be routed to to consensusApp
func TestRouting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// infoMtx blocks Info until we finish the test
	var infoMtx sync.Mutex
	infoMtx.Lock()
	infoExecuted := false

	logger := log.NewTestingLogger(t)

	defaultApp, defaultSocket := startApp(ctx, t, logger, "default")
	defer defaultApp.AssertExpectations(t)

	defaultApp.On("Info", mock.Anything, mock.Anything).Return(&types.ResponseInfo{
		Data: "info",
	}, nil).Run(func(_args mock.Arguments) {
		t.Log("Info: before lock")
		infoMtx.Lock()
		defer infoMtx.Unlock()
		t.Log("Info: after lock")
		infoExecuted = true
	}).Once()

	queryApp, querySocket := startApp(ctx, t, logger, "query")
	defer queryApp.AssertExpectations(t)
	queryApp.On("CheckTx", mock.Anything, mock.Anything).Return(&types.ResponseCheckTx{
		Priority: 1,
	}, nil).Once()
	queryApp.On("FinalizeBlock", mock.Anything, mock.Anything).Return(&types.ResponseFinalizeBlock{}, nil).Once()

	consensusApp, consensusSocket := startApp(ctx, t, logger, "consensus")
	defer consensusApp.AssertExpectations(t)
	consensusApp.On("PrepareProposal", mock.Anything, mock.Anything).Return(&types.ResponsePrepareProposal{
		AppHash:    []byte("apphash"),
		AppVersion: 1,
	}, nil).Once()
	consensusApp.On("FinalizeBlock", mock.Anything, mock.Anything).Return(&types.ResponseFinalizeBlock{
		RetainHeight: 1,
	}, nil).Once()

	addr := fmt.Sprintf("CheckTx:socket:%s", querySocket) +
		fmt.Sprintf(",FinalizeBlock:socket:%s,FinalizeBlock:socket:%s", querySocket, consensusSocket) +
		fmt.Sprintf(",PrepareProposal:socket:%s", consensusSocket) +
		fmt.Sprintf(",*:socket:%s", defaultSocket)

	logger.Info("configuring routed abci client with address", "addr", addr)
	routedClient, err := abciclient.NewRoutedClientWithAddr(logger, addr, true)
	assert.NoError(t, err)
	err = routedClient.Start(ctx)
	assert.NoError(t, err)

	// Test routing
	wg := sync.WaitGroup{}

	// Info is called from separate thread, as we want it to block
	// to see if we can execute other calls (on other clients) without blocking
	wg.Add(1)
	go func() {
		// info is locked, so it should finish last
		_, err := routedClient.Info(ctx, &types.RequestInfo{})
		require.NoError(t, err)
		wg.Done()
	}()

	// CheckTx
	_, err = routedClient.CheckTx(ctx, &types.RequestCheckTx{})
	assert.NoError(t, err)

	// FinalizeBlock
	_, err = routedClient.FinalizeBlock(ctx, &types.RequestFinalizeBlock{})
	assert.NoError(t, err)

	// PrepareProposal
	_, err = routedClient.PrepareProposal(ctx, &types.RequestPrepareProposal{})
	assert.NoError(t, err)

	// unlock info
	assert.False(t, infoExecuted)
	infoMtx.Unlock()
	wg.Wait()
	assert.True(t, infoExecuted)
}

func startApp(ctx context.Context, t *testing.T, logger log.Logger, id string) (*mocks.Application, string) {
	app := mocks.NewApplication(t)
	defer app.AssertExpectations(t)

	addr := fmt.Sprintf("unix://%s/%s", t.TempDir(), "/socket."+id)

	server, err := server.NewServer(logger, addr, "socket", app)
	require.NoError(t, err)
	err = server.Start(ctx)
	require.NoError(t, err)

	return app, addr
}

// / TestRoutedClientGrpc tests the RoutedClient correctly forwards requests to a gRPC server.
func TestRoutedClientGrpc(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	logger := log.NewTestingLogger(t)

	app := mocks.NewApplication(t)
	defer app.AssertExpectations(t)
	app.On("Echo", mock.Anything, mock.Anything).Return(
		func(_ctx context.Context, msg *types.RequestEcho) (*types.ResponseEcho, error) {
			return &types.ResponseEcho{Message: msg.Message}, nil
		}).Maybe()
	app.On("Info", mock.Anything, mock.Anything).Return(&types.ResponseInfo{}, nil).Once()

	grpcServer := server.NewGRPCServer(logger, "tcp://127.0.0.1:1234", app)
	require.NoError(t, grpcServer.Start(ctx))

	addr := "*:grpc:127.0.0.1:1234"
	logger.Info("configuring routed abci client with address", "addr", addr)
	client, err := abciclient.NewRoutedClientWithAddr(logger, addr, true)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))

	_, err = client.Info(ctx, &types.RequestInfo{})
	assert.NoError(t, err)

}

// A routed client picks the backends of each request by its name, so probing
// the backends PrepareProposal goes to is not Echo: a healthy default backend
// says nothing about a stalled proposal backend, nor a stalled default backend
// about a healthy proposal one.
func TestEchoRouteProbesTheBackendsOfTheMethod(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	logger := log.NewNopLogger()
	probe := func(client abciclient.Client, method abciclient.RequestType) error {
		ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		return abciclient.EchoRoute(ctx, client, method, "probe")
	}

	client, err := abciclient.NewRoutedClient(logger, echoBackend(t, false), abciclient.Routing{
		"PrepareProposal": {{Client: echoBackend(t, true), ClientID: "proposal"}},
	})
	require.NoError(t, err)
	_, err = client.Echo(ctx, "probe")
	require.NoError(t, err, "Echo goes to the healthy default backend")
	assert.ErrorIs(t, probe(client, "PrepareProposal"), context.DeadlineExceeded)

	client, err = abciclient.NewRoutedClient(logger, echoBackend(t, true), abciclient.Routing{
		"PrepareProposal": {
			{Client: echoBackend(t, false), ClientID: "proposal-1"},
			{Client: echoBackend(t, false), ClientID: "proposal-2"},
		},
	})
	require.NoError(t, err)
	assert.NoError(t, probe(client, "PrepareProposal"))
	assert.ErrorIs(t, probe(client, "Info"), context.DeadlineExceeded,
		"a method with no route of its own reaches the default backend")
}

// echoBackend returns an ABCI client whose Echo answers or, stalled, waits for
// the caller to give up.
func echoBackend(t *testing.T, stalled bool) *clientmocks.Client {
	backend := clientmocks.NewClient(t)
	backend.On("Echo", mock.Anything, mock.Anything).Return(
		func(ctx context.Context, msg string) (*types.ResponseEcho, error) {
			if stalled {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &types.ResponseEcho{Message: msg}, nil
		}).Maybe()
	return backend
}
