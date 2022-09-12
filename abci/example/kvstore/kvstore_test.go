package kvstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/example/code"
	abciserver "github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
)

const (
	testKey   = "abc"
	testValue = "def"
)

func testKVStore(ctx context.Context, t *testing.T, app types.Application, tx []byte, key, value string, height int64) {
	reqPrep := types.RequestPrepareProposal{
		Txs:    [][]byte{tx},
		Height: height,
	}

	respPrep, err := app.PrepareProposal(ctx, &reqPrep)
	require.NoError(t, err)
	require.Equal(t, 1, len(respPrep.TxResults))
	require.False(t, respPrep.TxResults[0].IsErr())

	reqFin := &types.RequestFinalizeBlock{Txs: [][]byte{tx}, AppHash: respPrep.AppHash}
	respFin, err := app.FinalizeBlock(ctx, reqFin)
	require.NoError(t, err)
	require.Equal(t, 0, len(respFin.Events))

	// repeating tx doesn't raise error
	respFin, err = app.FinalizeBlock(ctx, reqFin)
	require.NoError(t, err)
	require.Equal(t, 0, len(respFin.Events))

	// commit
	_, err = app.Commit(ctx)
	require.NoError(t, err)

	info, err := app.Info(ctx, &types.RequestInfo{})
	require.NoError(t, err)
	require.NotZero(t, info.LastBlockHeight)

	// make sure query is fine
	resQuery, err := app.Query(ctx, &types.RequestQuery{
		Path: "/store",
		Data: []byte(key),
	})

	require.NoError(t, err)
	require.Equal(t, code.CodeTypeOK, resQuery.Code)
	require.Equal(t, key, string(resQuery.Key))
	require.Equal(t, value, string(resQuery.Value))
	require.EqualValues(t, info.LastBlockHeight, resQuery.Height)

	// make sure proof is fine
	resQuery, err = app.Query(ctx, &types.RequestQuery{
		Path:  "/store",
		Data:  []byte(key),
		Prove: true,
	})
	require.NoError(t, err)
	require.EqualValues(t, code.CodeTypeOK, resQuery.Code)
	require.Equal(t, key, string(resQuery.Key))
	require.Equal(t, value, string(resQuery.Value))
	require.EqualValues(t, info.LastBlockHeight, resQuery.Height)
}

func TestKVStoreKV(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kvstore := NewApplication()
	key := testKey
	value := key
	tx := []byte(key)
	testKVStore(ctx, t, kvstore, tx, key, value, 1)

	value = testValue
	tx = []byte(key + "=" + value)
	testKVStore(ctx, t, kvstore, tx, key, value, 2)
}

func TestPersistentKVStoreKV(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	logger := log.NewNopLogger()

	kvstore := NewPersistentKVStoreApplication(logger, dir)
	key := testKey
	value := key
	tx := []byte(key)
	testKVStore(ctx, t, kvstore, tx, key, value, 1)

	value = testValue
	tx = []byte(key + "=" + value)
	testKVStore(ctx, t, kvstore, tx, key, value, 2)
}

func TestPersistentKVStoreInfo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	logger := log.NewNopLogger()

	kvstore := NewPersistentKVStoreApplication(logger, dir)
	if err := InitKVStore(ctx, kvstore); err != nil {
		t.Fatal(err)
	}
	height := int64(0)

	resInfo, err := kvstore.Info(ctx, &types.RequestInfo{})
	if err != nil {
		t.Fatal(err)
	}

	if resInfo.LastBlockHeight != height {
		t.Fatalf("expected height of %d, got %d", height, resInfo.LastBlockHeight)
	}

	// make and apply block
	height = int64(1)
	makeApplyBlock(ctx, t, kvstore, int(height))

	resInfo, err = kvstore.Info(ctx, &types.RequestInfo{})
	require.NoError(t, err)
	require.Equal(t, resInfo.LastBlockHeight, height, "expected height of %d, got %d", height, resInfo.LastBlockHeight)
}

// add a validator, remove a validator, update a validator
func TestValUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kvstore := NewApplication()

	// init with some validators
	total := 10
	nInit := 5
	fullVals := RandValidatorSetUpdate(total)
	initVals := RandValidatorSetUpdate(nInit)

	require.NotEqual(t, fullVals.QuorumHash, initVals.QuorumHash)

	// initialize with the first nInit
	_, err := kvstore.InitChain(ctx, &types.RequestInitChain{
		ValidatorSet: &initVals,
	})
	require.NoError(t, err)
	kvstore.AddValidatorSetUpdate(fullVals, 2)
	resp, _ := makeApplyBlock(ctx, t, kvstore, 1)
	require.Equal(t, initVals.QuorumHash, resp.ValidatorSetUpdate.QuorumHash)
	resp, _ = makeApplyBlock(ctx, t, kvstore, 2)
	require.Equal(t, fullVals.QuorumHash, resp.ValidatorSetUpdate.QuorumHash)
}

func makeApplyBlock(
	ctx context.Context,
	t *testing.T,
	kvstore types.Application,
	heightInt int,
	txs ...[]byte,
) (*types.ResponseProcessProposal, *types.ResponseFinalizeBlock) {
	// make and apply block
	height := int64(heightInt)
	hash := []byte("foo")

	respProcessProposal, err := kvstore.ProcessProposal(ctx, &types.RequestProcessProposal{
		Hash:   hash,
		Height: height,
		Txs:    txs,
	})
	require.NoError(t, err)
	require.NotZero(t, respProcessProposal)
	require.Equal(t, types.ResponseProcessProposal_ACCEPT, respProcessProposal.Status)

	resFinalizeBlock, err := kvstore.FinalizeBlock(ctx, &types.RequestFinalizeBlock{
		Hash:    hash,
		Height:  height,
		Txs:     txs,
		AppHash: respProcessProposal.AppHash,
	})
	require.NoError(t, err)
	require.Len(t, resFinalizeBlock.Events, 0)

	_, err = kvstore.Commit(ctx)
	require.NoError(t, err)
	return respProcessProposal, resFinalizeBlock
}

func makeSocketClientServer(
	ctx context.Context,
	t *testing.T,
	logger log.Logger,
	app types.Application,
	name string,
) (abciclient.Client, service.Service, error) {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	t.Cleanup(leaktest.Check(t))

	// Start the listener
	socket := fmt.Sprintf("unix://%s.sock", name)

	server := abciserver.NewSocketServer(logger.With("module", "abci-server"), socket, app)
	if err := server.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}

	// Connect to the socket
	client := abciclient.NewSocketClient(logger.With("module", "abci-client"), socket, false)
	if err := client.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}

	return client, server, nil
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

func TestClientServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewTestingLogger(t)

	// set up socket app
	kvstore := NewApplication()
	client, server, err := makeSocketClientServer(ctx, t, logger, kvstore, "kvstore-socket")
	require.NoError(t, err)
	t.Cleanup(func() { cancel(); server.Wait() })
	t.Cleanup(func() { cancel(); client.Wait() })

	runClientTests(ctx, t, client)

	// set up grpc app
	kvstore = NewApplication()
	gclient, gserver, err := makeGRPCClientServer(ctx, t, logger, kvstore, "/tmp/kvstore-grpc")
	require.NoError(t, err)

	t.Cleanup(func() { cancel(); gserver.Wait() })
	t.Cleanup(func() { cancel(); gclient.Wait() })

	runClientTests(ctx, t, gclient)
}

func runClientTests(ctx context.Context, t *testing.T, client abciclient.Client) {
	// run some tests....
	key := testKey
	value := key
	tx := []byte(key)
	testClient(ctx, t, client, 1, tx, key, value)

	value = testValue
	tx = []byte(key + "=" + value)
	testClient(ctx, t, client, 2, tx, key, value)
}

func testClient(ctx context.Context, t *testing.T, app abciclient.Client, height int64, tx []byte, key, value string) {
	rpp, err := app.ProcessProposal(ctx, &types.RequestProcessProposal{
		Txs:    [][]byte{tx},
		Height: height,
	})
	require.NoError(t, err)
	require.NotZero(t, rpp)
	require.Equal(t, 1, len(rpp.TxResults))
	require.False(t, rpp.TxResults[0].IsErr())

	ar, err := app.FinalizeBlock(ctx, &types.RequestFinalizeBlock{
		Txs:     [][]byte{tx},
		AppHash: rpp.AppHash,
	})
	require.NoError(t, err)
	require.Zero(t, ar.RetainHeight)
	require.Empty(t, ar.Events)

	// repeating FinalizeBlock doesn't raise error
	ar, err = app.FinalizeBlock(ctx, &types.RequestFinalizeBlock{
		Txs:     [][]byte{tx},
		AppHash: rpp.AppHash,
	})
	require.NoError(t, err)
	assert.Zero(t, ar.RetainHeight)
	assert.Empty(t, ar.Events)

	// commit
	_, err = app.Commit(ctx)
	require.NoError(t, err)

	info, err := app.Info(ctx, &types.RequestInfo{})
	require.NoError(t, err)
	require.NotZero(t, info.LastBlockHeight)

	// make sure query is fine
	resQuery, err := app.Query(ctx, &types.RequestQuery{
		Path: "/store",
		Data: []byte(key),
	})
	require.NoError(t, err)
	require.Equal(t, code.CodeTypeOK, resQuery.Code)
	require.Equal(t, key, string(resQuery.Key))
	require.Equal(t, value, string(resQuery.Value))
	require.EqualValues(t, info.LastBlockHeight, resQuery.Height)

	// make sure proof is fine
	resQuery, err = app.Query(ctx, &types.RequestQuery{
		Path:  "/store",
		Data:  []byte(key),
		Prove: true,
	})
	require.NoError(t, err)
	require.Equal(t, code.CodeTypeOK, resQuery.Code)
	require.Equal(t, key, string(resQuery.Key))
	require.Equal(t, value, string(resQuery.Value))
	require.EqualValues(t, info.LastBlockHeight, resQuery.Height)
}
