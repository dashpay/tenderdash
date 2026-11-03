package state_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/eventbus"
	mpmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// randomnessApp is testApp answering ApplyBlockRandomness with a fixed app hash, and recording the calls
// and the block ID FinalizeBlock receives.
type randomnessApp struct {
	testApp

	appHash         []byte
	randomnessCalls int
	finalizeBlockID *tmproto.BlockID
}

func (app *randomnessApp) ApplyBlockRandomness(_ context.Context, _ *abci.RequestApplyBlockRandomness) (*abci.ResponseApplyBlockRandomness, error) {
	app.randomnessCalls++
	return &abci.ResponseApplyBlockRandomness{AppHash: app.appHash}, nil
}

func (app *randomnessApp) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	app.finalizeBlockID = req.BlockID
	return app.testApp.FinalizeBlock(ctx, req)
}

// TestFinalizeBlockRandomBeacon checks FinalizeBlock at consensus version 2: it applies the commit's beacon
// before FinalizeBlock reaches the application, refuses an app hash that differs from the commit's, sends
// and records the commit's BlockID, and keeps the commit's app hash and beacon in the new state. Before
// consensus version 2 none of this happens.
func TestFinalizeBlockRandomBeacon(t *testing.T) {
	finalAppHash := crypto.Checksum([]byte("final app hash"))
	beacon := crypto.CRandBytes(types.SignatureSize)

	setup := func(t *testing.T, consensusVersion int32) (context.Context, *randomnessApp, *sm.BlockExecutor, sm.State, *types.Block, sm.CurrentRoundState) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		app := &randomnessApp{appHash: finalAppHash}
		logger := log.NewNopLogger()
		proxyApp := proxy.New(abciclient.NewLocalClient(logger, app), logger, proxy.NopMetrics())
		require.NoError(t, proxyApp.Start(ctx))

		state, stateDB, _ := makeState(t, 1, 1)
		state.ConsensusParams.Version.ConsensusVersion = consensusVersion
		nodeProTxHash := state.Validators.Validators[0].ProTxHash
		ctx = dash.ContextWithProTxHash(ctx, nodeProTxHash)

		mp := &mpmocks.Mempool{}
		mp.On("Lock").Return()
		mp.On("Unlock").Return()
		mp.On("FlushAppConn", mock.Anything).Return(nil)
		mp.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
			mock.Anything).Return(nil)
		mp.On("ReapMaxBytesMaxGas", mock.Anything, mock.Anything).Return(types.Txs{})
		eventBus := eventbus.NewDefault(logger)
		require.NoError(t, eventBus.Start(ctx))
		blockExec := sm.NewBlockExecutor(sm.NewStore(stateDB), proxyApp, mp, sm.EmptyEvidencePool{},
			store.NewBlockStore(dbm.NewMemDB()), eventBus)

		block, uncommittedState, err := blockExec.CreateProposalBlock(ctx, 1, 0, state,
			types.NewCommit(state.LastBlockHeight, 0, state.LastBlockID, nil, nil), nodeProTxHash, 1)
		require.NoError(t, err)
		return ctx, app, blockExec, state, block, uncommittedState
	}
	commitFor := func(block *types.Block, appHash []byte) *types.Commit {
		blockID := block.BlockID(nil)
		blockID.StateID = block.Header.StateIDWithAppHash(appHash).Hash()
		return &types.Commit{Height: block.Height, BlockID: blockID, Beacon: beacon, AppHash: appHash}
	}

	t.Run("consensus version 2", func(t *testing.T) {
		ctx, app, blockExec, state, block, uncommittedState := setup(t, 2)
		commit := commitFor(block, finalAppHash)
		next, _, err := blockExec.FinalizeBlock(ctx, state, uncommittedState, block.BlockID(nil), block, commit,
			types.VerifiedCommit{})
		require.NoError(t, err)
		require.Equal(t, 1, app.randomnessCalls)
		require.NotNil(t, app.finalizeBlockID)
		require.EqualValues(t, commit.BlockID.StateID, app.finalizeBlockID.StateID, "FinalizeBlock gets the commit's BlockID")
		require.True(t, next.LastBlockID.Equals(commit.BlockID))
		require.EqualValues(t, finalAppHash, next.LastAppHash)
		require.Equal(t, beacon, next.LastBlockBeacon)
	})

	t.Run("consensus version 2, the application diverged", func(t *testing.T) {
		ctx, app, blockExec, state, block, uncommittedState := setup(t, 2)
		otherAppHash := crypto.Checksum([]byte("other app hash"))
		commit := commitFor(block, otherAppHash)
		_, _, err := blockExec.FinalizeBlock(ctx, state, uncommittedState, block.BlockID(nil), block, commit,
			types.VerifiedCommit{})
		require.ErrorIs(t, err, sm.ErrAppHashMismatch)
		require.Nil(t, app.finalizeBlockID, "the block does not reach FinalizeBlock")
	})

	t.Run("before consensus version 2", func(t *testing.T) {
		ctx, app, blockExec, state, block, uncommittedState := setup(t, 1)
		blockID := block.BlockID(nil)
		next, _, err := blockExec.FinalizeBlock(ctx, state, uncommittedState, blockID, block,
			&types.Commit{Height: block.Height, BlockID: blockID}, types.VerifiedCommit{})
		require.NoError(t, err)
		require.Zero(t, app.randomnessCalls)
		require.True(t, next.LastBlockID.Equals(blockID))
		require.EqualValues(t, block.AppHash, next.LastAppHash)
		require.Empty(t, next.LastBlockBeacon)
	})
}

// TestVerifyLastCommitBeacon checks that, once the last block was committed at consensus version 2, the
// next block's LastCommit must carry the beacon and the app hash of the commit the last block was
// finalized with, and that both are ignored before.
func TestVerifyLastCommitBeacon(t *testing.T) {
	beacon := crypto.CRandBytes(types.SignatureSize)
	appHash := crypto.Checksum([]byte("final app hash"))
	block := &types.Block{LastCommit: &types.Commit{Beacon: beacon, AppHash: appHash}}

	state := sm.State{LastBlockBeacon: beacon, LastAppHash: appHash}
	require.NoError(t, sm.VerifyLastCommitBeacon(state, block))

	state.LastBlockBeacon = crypto.CRandBytes(types.SignatureSize)
	require.ErrorContains(t, sm.VerifyLastCommitBeacon(state, block), "LastCommit.Beacon")

	state = sm.State{LastBlockBeacon: beacon, LastAppHash: crypto.Checksum([]byte("other"))}
	require.ErrorContains(t, sm.VerifyLastCommitBeacon(state, block), "LastCommit.AppHash")

	// Before consensus version 2 the fields are not checked, whatever they hold.
	require.NoError(t, sm.VerifyLastCommitBeacon(sm.State{LastAppHash: crypto.Checksum([]byte("other"))}, block))
}
