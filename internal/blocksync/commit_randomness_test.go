package blocksync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/eventbus"
	mpmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

// TestBlockResponseRefusesPaddedCommitBeacon is the regression test for a block sync peer relaying an
// honest block and its validly signed commit with the commit's beacon padded: neither the commit hash
// nor its threshold signature covers the beacon, so before the size bound the padded commit passed
// verification, was stored as the seen commit and reached the application. The response is now
// refused while it is decoded, before anything is verified, stored or passed on.
func TestBlockResponseRefusesPaddedCommitBeacon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	block, _, _, seen := makeNextBlock(ctx, t, state, privVals[0], 1, types.NewCommit(0, 0, types.BlockID{}, nil, nil))

	response := func(beacon, appHash []byte) (*BlockResponse, error) {
		t.Helper()
		pbBlock, err := block.ToProto()
		require.NoError(t, err)
		pbCommit := seen.ToProto()
		pbCommit.Beacon = beacon
		pbCommit.AppHash = appHash
		bz, err := (&bcproto.BlockResponse{Block: pbBlock, Commit: pbCommit}).Marshal()
		require.NoError(t, err)
		var decoded bcproto.BlockResponse
		require.NoError(t, decoded.Unmarshal(bz))
		return BlockResponseFromProto(&decoded, "peer")
	}

	honest, err := response(nil, nil)
	require.NoError(t, err)
	require.Equal(t, seen.Hash(), honest.Commit.Hash())

	for name, fields := range map[string][2][]byte{
		"25 MiB beacon":           {make([]byte, 25<<20), []byte("junk")},
		"5 MiB beacon":            {make([]byte, 5<<20), make([]byte, 32)},
		"app hash without beacon": {nil, make([]byte, 32)},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := response(fields[0], fields[1])
			require.ErrorIs(t, err, types.ErrInvalidCommitRandomness)
			require.Nil(t, resp)
		})
	}
}

// finalizeRecorder records the last commit FinalizeBlock received.
type finalizeRecorder struct {
	abci.BaseApplication
	commit abci.CommitInfo
}

func (r *finalizeRecorder) FinalizeBlock(_ context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	r.commit = req.Commit
	return &abci.ResponseFinalizeBlock{}, nil
}

// TestBlockSyncDropsRelayedCommitRandomness is the regression test for a block sync peer relaying an
// honest block and its validly signed commit with a correctly sized beacon and app hash added: neither
// is covered by the commit's hash or threshold signature, so the commit passes verification. Before
// consensus version 2 a commit carries neither, and the applier drops both, so they are neither passed
// to the application nor stored as the seen commit this node serves.
func TestBlockSyncDropsRelayedCommitRandomness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	stateStore := sm.NewStore(dbm.NewMemDB())
	require.NoError(t, stateStore.Save(state))
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	app := &finalizeRecorder{}
	proxyApp := proxy.New(abciclient.NewLocalClient(logger, app), logger, proxy.NopMetrics())
	require.NoError(t, proxyApp.Start(ctx))
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(nil)
	blockExec := sm.NewBlockExecutor(stateStore, proxyApp, mp, sm.EmptyEvidencePool{}, blockStore, eventBus)
	applier := newBlockApplier(blockExec, blockStore, applierWithState(state))

	block, _, _, seen := makeNextBlock(ctx, t, state, privVals[0], 1, types.NewCommit(0, 0, types.BlockID{}, nil, nil))
	pbBlock, err := block.ToProto()
	require.NoError(t, err)
	pbCommit := seen.ToProto()
	pbCommit.Beacon = crypto.CRandBytes(types.SignatureSize)
	pbCommit.AppHash = crypto.CRandBytes(crypto.DefaultAppHashSize)
	resp, err := BlockResponseFromProto(&bcproto.BlockResponse{Block: pbBlock, Commit: pbCommit}, "peer")
	require.NoError(t, err, "a correctly sized beacon and app hash pass the size bound")

	require.NoError(t, applier.Apply(ctx, resp.Block, resp.Commit))
	require.Equal(t, seen.ToCommitInfo(), app.commit)
	stored := blockStore.LoadSeenCommitAt(1)
	require.Empty(t, stored.Beacon)
	require.Empty(t, stored.AppHash)
	require.Equal(t, seen.Hash(), stored.Hash())
}
