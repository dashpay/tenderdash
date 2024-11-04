package kvstore

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/code"
	abciserver "github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	tmcrypto "github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/libs/service"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	pbversion "github.com/dashpay/tenderdash/proto/tendermint/version"
	tmtypes "github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

const (
	testKey   = "abc"
	testValue = "def"
)

func testKVStore(ctx context.Context, t *testing.T, app types.Application, tx []byte, key, value string, height int64) {
	reqPrep := types.RequestPrepareProposal{
		Txs:        [][]byte{tx},
		Height:     height,
		MaxTxBytes: 40960,
	}

	respPrep, err := app.PrepareProposal(ctx, &reqPrep)
	require.NoError(t, err)
	assert.Len(t, respPrep.TxRecords, 1)
	require.Equal(t, 1, len(respPrep.TxResults))
	require.False(t, respPrep.TxResults[0].IsErr(), respPrep.TxResults[0].Log)

	// Duplicate PrepareProposal should return error
	_, err = app.PrepareProposal(ctx, &reqPrep)
	require.ErrorContains(t, err, "duplicate PrepareProposal call")

	reqProcess := &types.RequestProcessProposal{
		Txs:     [][]byte{tx},
		Height:  height,
		Version: &pbversion.Consensus{App: tmmath.MustConvertUint64(height)},
	}
	respProcess, err := app.ProcessProposal(ctx, reqProcess)
	require.NoError(t, err)
	require.Len(t, respProcess.TxResults, 1)
	require.False(t, respProcess.TxResults[0].IsErr(), respProcess.TxResults[0].Log)
	require.Len(t, respProcess.Events, 1)

	// Duplicate ProcessProposal calls should return error
	_, err = app.ProcessProposal(ctx, reqProcess)
	require.ErrorContains(t, err, "duplicate ProcessProposal call")

	reqFin := &types.RequestFinalizeBlock{Height: height}
	reqFin.Block, reqFin.BlockID = makeBlock(t, height, [][]byte{tx}, respPrep.AppHash)
	_, err = app.FinalizeBlock(ctx, reqFin)
	require.NoError(t, err)

	// repeating tx raises an error
	_, err = app.FinalizeBlock(ctx, reqFin)
	require.Error(t, err)

	info, err := app.Info(ctx, &types.RequestInfo{})
	require.NoError(t, err)
	require.NotZero(t, info.LastBlockHeight)
	assertRespInfo(t, height, respPrep.AppHash, *info)

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

	kvstore := newKvApp(ctx, t, 1)
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

	kvstore, err := NewPersistentApp(DefaultConfig(dir),
		WithLogger(logger.With("module", "kvstore")),
		WithAppVersion(0))
	require.NoError(t, err)

	key := testKey
	value := key
	tx := []byte(key)
	testKVStore(ctx, t, kvstore, tx, key, value, 1)

	value = testValue
	tx = []byte(key + "=" + value)
	testKVStore(ctx, t, kvstore, tx, key, value, 2)

	data, err := os.ReadFile(path.Join(dir, "state.json"))
	require.NoError(t, err)

	assert.Contains(t, string(data), fmt.Sprintf(`"key":"%s","value":"%s"`, key, value))
}

func TestPersistentKVStoreInfo(t *testing.T) {
	type testCase struct {
		InitialHeight int64
	}
	testCases := []testCase{
		{InitialHeight: 0},
		{InitialHeight: 1},
		{InitialHeight: 1000},
	}

	for _, tc := range testCases {
		t.Run(strconv.Itoa(int(tc.InitialHeight)), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dir := t.TempDir()
			logger := log.NewTestingLogger(t).With("module", "kvstore")

			kvstore, err := NewPersistentApp(DefaultConfig(dir), WithLogger(logger), WithAppVersion(0))
			require.NoError(t, err)
			defer kvstore.Close()

			// Initialize the blockchain
			vset := RandValidatorSetUpdate(1)
			reqInitChain := types.RequestInitChain{
				ValidatorSet:  &vset,
				InitialHeight: tc.InitialHeight,
			}
			_, err = kvstore.InitChain(ctx, &reqInitChain)
			require.NoError(t, err, "InitChain()")

			respInfo, err := kvstore.Info(ctx, &types.RequestInfo{})
			require.NoError(t, err, "Info()")
			// we are at genesis, so the height should always be 0
			assertRespInfo(t, 0, nil, *respInfo)

			// make and apply block
			height := tc.InitialHeight
			if height == 0 {
				height = 1
			}
			rpp, _ := makeApplyBlock(ctx, t, kvstore, int(height))

			respInfo, err = kvstore.Info(ctx, &types.RequestInfo{})
			require.NoError(t, err)
			assertRespInfo(t, height, rpp.AppHash, *respInfo)

			assert.Equal(t, respInfo.LastBlockHeight, height, "expected height of %d, got %d", height, respInfo.LastBlockHeight)
		})
	}
}

// TestConsensusParamsUpdate checks if consensus params are updated correctly
func TestConsensusParamsUpdate(t *testing.T) {
	const genesisHeight = int64(100)
	type testCase struct {
		ConsParamUpdates *tmproto.ConsensusParams
	}
	testCases := map[int64]testCase{
		genesisHeight: {
			ConsParamUpdates: &tmproto.ConsensusParams{
				Abci: &tmproto.ABCIParams{RecheckTx: true},
			},
		},
		genesisHeight + 3: {
			ConsParamUpdates: &tmproto.ConsensusParams{
				Abci: &tmproto.ABCIParams{RecheckTx: false},
			},
		},
		genesisHeight + 4: {
			ConsParamUpdates: &tmproto.ConsensusParams{
				Version: &tmproto.VersionParams{
					AppVersion: 123,
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kvstore := newKvApp(ctx, t, genesisHeight)

	maxHeight := genesisHeight
	for height, tc := range testCases {
		kvstore.AddConsensusParamsUpdate(*tc.ConsParamUpdates, height)
		if height > maxHeight {
			maxHeight = height
		}
	}

	for height := genesisHeight; height <= maxHeight; height++ {
		respProcess, _ := makeApplyBlock(ctx, t, kvstore, int(height))
		assert.EqualValues(t, testCases[height].ConsParamUpdates, respProcess.ConsensusParamUpdates)
	}
}

// add a validator, remove a validator, update a validator
func TestValUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kvstore, err := NewMemoryApp(WithAppVersion(0))
	require.NoError(t, err)

	// init with some validators
	total := 10
	nInit := 5
	fullVals := RandValidatorSetUpdate(total)
	initVals := RandValidatorSetUpdate(nInit)

	require.NotEqual(t, fullVals.QuorumHash, initVals.QuorumHash)

	// initialize with the first nInit
	_, err = kvstore.InitChain(ctx, &types.RequestInitChain{
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

	for _, tx := range txs {
		respCheck, err := kvstore.CheckTx(ctx, &types.RequestCheckTx{Tx: tx, Type: types.CheckTxType_New})
		require.NoError(t, err)
		assert.Equal(t, code.CodeTypeOK, respCheck.Code)
	}

	respProcessProposal, err := kvstore.ProcessProposal(ctx, &types.RequestProcessProposal{
		Hash:    hash,
		Height:  height,
		Txs:     txs,
		Version: &pbversion.Consensus{App: tmmath.MustConvertUint64(height)},
	})
	require.NoError(t, err)
	require.NotZero(t, respProcessProposal)
	require.Equal(t, types.ResponseProcessProposal_ACCEPT, respProcessProposal.Status)
	require.Len(t, respProcessProposal.Events, 1)

	rfb := &types.RequestFinalizeBlock{Hash: hash, Height: height}
	rfb.Block, rfb.BlockID = makeBlock(t, height, txs, respProcessProposal.AppHash)
	resFinalizeBlock, err := kvstore.FinalizeBlock(ctx, rfb)
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

	client := abciclient.NewGRPCClient(logger.With("module", "abci-client"), socket, nil, true)

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
	kvstore, err := NewMemoryApp(
		WithLogger(logger.With("module", "app")),
		WithAppVersion(0))
	require.NoError(t, err)

	client, server, err := makeSocketClientServer(ctx, t, logger, kvstore, "kvstore-socket")
	require.NoError(t, err)

	t.Cleanup(func() { cancel(); server.Wait() })
	t.Cleanup(func() { cancel(); client.Wait() })

	runClientTests(ctx, t, client)

	// set up grpc app
	kvstore, err = NewMemoryApp(WithAppVersion(0))
	require.NoError(t, err)

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
		Txs:     [][]byte{tx},
		Height:  height,
		Version: &pbversion.Consensus{App: tmmath.MustConvertUint64(height)},
	})
	require.NoError(t, err)
	require.NotZero(t, rpp)
	require.Equal(t, 1, len(rpp.TxResults))
	require.False(t, rpp.TxResults[0].IsErr())
	require.Len(t, rpp.Events, 1)

	rfb := &types.RequestFinalizeBlock{Height: height}
	rfb.Block, rfb.BlockID = makeBlock(t, height, [][]byte{tx}, rpp.AppHash)
	ar, err := app.FinalizeBlock(ctx, rfb)
	require.NoError(t, err)
	require.Zero(t, ar.RetainHeight)

	info, err := app.Info(ctx, &types.RequestInfo{})
	require.NoError(t, err)
	require.NotZero(t, info.LastBlockHeight)
	assertRespInfo(t, height, rpp.AppHash, *info)

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

func TestSnapshots(t *testing.T) {
	const (
		genesisHeight    = int64(1000)
		nSnapshots       = 3
		snapshotInterval = 3
	)
	maxHeight := genesisHeight + nSnapshots*snapshotInterval
	appHashes := make(map[int64]tmbytes.HexBytes, maxHeight-genesisHeight+1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := DefaultConfig(t.TempDir())
	cfg.SnapshotInterval = snapshotInterval
	app := newKvApp(ctx, t, genesisHeight, WithConfig(cfg))

	for height := genesisHeight; height <= maxHeight; height++ {
		txs := [][]byte{
			[]byte(fmt.Sprintf("lastHeight=%d", height)),
			[]byte(fmt.Sprintf("tx%d=%d", height-genesisHeight+1, height)),
		}
		rpp, rfb := makeApplyBlock(ctx, t, app, int(height), txs...)
		assert.NotNil(t, rpp)
		assert.NotNil(t, rfb)
		appHashes[height] = rpp.AppHash
	}

	snapshots, err := app.ListSnapshots(ctx, &types.RequestListSnapshots{})
	require.NoError(t, err)
	assert.Len(t, snapshots.Snapshots, nSnapshots)

	recentSnapshot := snapshots.Snapshots[len(snapshots.Snapshots)-1]
	snapshotHeight := int64(recentSnapshot.Height)
	assert.Equal(t, maxHeight-(maxHeight%snapshotInterval), snapshotHeight)

	// Now, let's emulate state sync with the most recent snapshot
	dstApp := newKvApp(ctx, t, genesisHeight)

	respOffer, err := dstApp.OfferSnapshot(ctx, &types.RequestOfferSnapshot{
		Snapshot: recentSnapshot,
		AppHash:  appHashes[snapshotHeight],
	})
	require.NoError(t, err)
	assert.Equal(t, types.ResponseOfferSnapshot_ACCEPT, respOffer.Result)
	loaded, err := app.LoadSnapshotChunk(ctx, &types.RequestLoadSnapshotChunk{
		Height:  recentSnapshot.Height,
		ChunkId: recentSnapshot.Hash,
		Version: recentSnapshot.Version,
	})
	require.NoError(t, err)

	applied, err := dstApp.ApplySnapshotChunk(ctx, &types.RequestApplySnapshotChunk{
		ChunkId: recentSnapshot.Hash,
		Chunk:   loaded.Chunk,
		Sender:  "app",
	})
	require.NoError(t, err)
	assert.Equal(t, types.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT, applied.Result)
	infoResp, err := dstApp.Info(ctx, &types.RequestInfo{})
	require.NoError(t, err)
	assertRespInfo(t, int64(recentSnapshot.Height), appHashes[snapshotHeight], *infoResp)

	respQuery, err := dstApp.Query(ctx, &types.RequestQuery{Data: []byte("lastHeight")})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", recentSnapshot.Height), string(respQuery.Value))
}

func newKvApp(ctx context.Context, t *testing.T, genesisHeight int64, opts ...OptFunc) *Application {
	defaultOpts := []OptFunc{
		WithValidatorSetUpdates(map[int64]types.ValidatorSetUpdate{
			genesisHeight: RandValidatorSetUpdate(1),
		}),
		WithAppVersion(0),
	}
	app, err := NewMemoryApp(append(defaultOpts, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })

	reqInitChain := &types.RequestInitChain{
		InitialHeight: genesisHeight,
	}
	_, err = app.InitChain(ctx, reqInitChain)
	require.NoError(t, err)

	return app
}

func assertRespInfo(t *testing.T, expectLastBlockHeight int64, expectAppHash tmbytes.HexBytes, actual types.ResponseInfo, msgs ...interface{}) {
	t.Helper()

	if expectAppHash == nil {
		expectAppHash = make(tmbytes.HexBytes, tmcrypto.DefaultAppHashSize)
	}
	expected := types.ResponseInfo{
		LastBlockHeight:  expectLastBlockHeight,
		LastBlockAppHash: expectAppHash,
		Version:          version.ABCIVersion,
		AppVersion:       tmmath.MustConvertUint64(expectLastBlockHeight + 1),
		Data:             fmt.Sprintf(`{"appHash":"%s"}`, expectAppHash.String()),
	}

	assert.Equal(t, expected, actual, msgs...)
}

func makeBlock(t *testing.T, height int64, txs [][]byte, appHash []byte) (*tmproto.Block, *tmproto.BlockID) {
	block := tmtypes.MakeBlock(height, bytes2Txs(txs), &tmtypes.Commit{}, nil)
	block.Header.AppHash = appHash
	pbBlock, err := block.ToProto()
	require.NoError(t, err)
	blockID := block.BlockID(nil)
	pbBlockID := blockID.ToProto()
	return pbBlock, &pbBlockID
}

func bytes2Txs(items [][]byte) []tmtypes.Tx {
	txs := make([]tmtypes.Tx, len(items))
	for i, item := range items {
		txs[i] = item
	}
	return txs
}
