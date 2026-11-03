package consensus

import (
	"bytes"
	"context"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	sm "github.com/dashpay/tenderdash/internal/state"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// randomBeaconApp is the kvstore application recording the order of the calls made around a precommit.
type randomBeaconApp struct {
	*kvstore.Application

	mtx   sync.Mutex
	calls []string
}

func (app *randomBeaconApp) record(call string) {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	app.calls = append(app.calls, call)
}

func (app *randomBeaconApp) recorded() []string {
	app.mtx.Lock()
	defer app.mtx.Unlock()
	return append([]string(nil), app.calls...)
}

func (app *randomBeaconApp) ApplyBlockRandomness(ctx context.Context, req *abci.RequestApplyBlockRandomness) (*abci.ResponseApplyBlockRandomness, error) {
	app.record("apply_block_randomness")
	return app.Application.ApplyBlockRandomness(ctx, req)
}

func (app *randomBeaconApp) ExtendVote(ctx context.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	app.record("extend_vote")
	return app.Application.ExtendVote(ctx, req)
}

func (app *randomBeaconApp) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	app.record("finalize_block")
	return app.Application.FinalizeBlock(ctx, req)
}

// TestRandomBeaconConsensusVersion2 runs a single validator at consensus version 2 and checks the whole
// chain of the random beacon: the precommit signs the state ID of the app hash ApplyBlockRandomness
// returned, which the kvstore example makes differ from the header's; ApplyBlockRandomness runs before
// ExtendVote; the commit carries the verified beacon and that app hash; the state and the next header
// name the commit's BlockID; and the next block, whose LastCommit carries them, validates and commits.
func TestRandomBeaconConsensusVersion2(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	kvApp, err := kvstore.NewMemoryApp()
	require.NoError(t, err)
	app := &randomBeaconApp{Application: kvApp}
	params := factory.ConsensusParams(func(cp *types.ConsensusParams) {
		cp.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
	})
	cs, _ := makeState(ctx, t, makeStateArgs{validators: 1, application: app, consensusParams: params})
	stateData := cs.GetStateData()
	height, round := stateData.Height, stateData.Round

	voteCh := subscribe(ctx, t, cs.eventBus, types.EventQueryVote)
	propCh := subscribe(ctx, t, cs.eventBus, types.EventQueryCompleteProposal)
	newRoundCh := subscribe(ctx, t, cs.eventBus, types.EventQueryNewRound)

	startTestRound(ctx, cs, height, round)
	ensureNewRound(t, newRoundCh, height, round)
	propBlockID := ensureNewProposal(t, propCh, height, round)

	prevote := ensureMessageBeforeTimeout(t, voteCh, ensureTimeout).Data().(types.EventDataVote).Vote
	require.Equal(t, tmproto.PrevoteType, prevote.Type)
	require.True(t, prevote.BlockID.Equals(propBlockID), "the prevote signs the header's state ID")

	precommit := ensureMessageBeforeTimeout(t, voteCh, ensureTimeout).Data().(types.EventDataVote).Vote
	require.Equal(t, tmproto.PrecommitType, precommit.Type)
	require.True(t, precommit.BlockID.SameBlock(propBlockID))
	require.False(t, precommit.BlockID.Equals(propBlockID), "the precommit signs the state ID after the randomness")

	ensureNewRound(t, newRoundCh, height+1, 0)

	block := cs.blockStore.LoadBlock(height)
	require.NotNil(t, block)
	commit := cs.blockStore.LoadSeenCommit()
	require.NotNil(t, commit)
	require.Equal(t, height, commit.Height)
	require.True(t, commit.BlockID.Equals(precommit.BlockID))
	require.Len(t, commit.Beacon, types.SignatureSize)
	require.NoError(t, commit.VerifyRandomBeacon(stateData.state.ChainID, stateData.Validators, &block.Header))
	require.EqualValues(t, crypto.Checksum(append(block.AppHash.Copy(), commit.Beacon...)), commit.AppHash,
		"the kvstore example's translation of the header app hash with the beacon")
	require.False(t, bytes.Equal(block.AppHash, commit.AppHash))

	calls := app.recorded()
	require.Equal(t, []string{"apply_block_randomness", "extend_vote"}, calls[:2],
		"the randomness is applied before the vote is extended")
	require.Contains(t, calls, "finalize_block")

	next := cs.GetStateData()
	require.True(t, next.state.LastBlockID.Equals(commit.BlockID), "the state names the commit's BlockID")
	require.EqualValues(t, commit.AppHash, next.state.LastAppHash)
	require.Equal(t, commit.Beacon, next.state.LastBlockBeacon)

	info, err := kvApp.Info(ctx, &abci.RequestInfo{})
	require.NoError(t, err)
	require.EqualValues(t, commit.AppHash, info.LastBlockAppHash, "the application committed the final app hash")

	// The next block carries the commit as its LastCommit and the commit's BlockID as its LastBlockID.
	ensureNewRound(t, newRoundCh, height+2, 0)
	nextBlock := cs.blockStore.LoadBlock(height + 1)
	require.NotNil(t, nextBlock)
	require.True(t, nextBlock.LastBlockID.Equals(commit.BlockID))
	require.Equal(t, commit.Beacon, nextBlock.LastCommit.Beacon)
	require.EqualValues(t, commit.AppHash, nextBlock.LastCommit.AppHash)
}

// TestReactorRandomBeaconConsensusVersion2 runs four validators at consensus version 2, where every
// precommit needs the threshold signature recovered from the other validators' prevotes, and checks that
// all of them commit the same beacon and final app hash, which verify against the block.
func TestReactorRandomBeaconConsensusVersion2(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const n = 4
	params := factory.ConsensusParams(func(cp *types.ConsensusParams) {
		cp.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
	})
	states := makeConsensusStateWithParams(ctx, t, configSetup(t), n, "consensus_random_beacon_test",
		newTickerFunc(), params)
	rts := setup(ctx, t, n, states, 100)
	rts.switchToConsensus(ctx)

	const lastHeight = int64(3)
	require.Eventually(t, func() bool {
		for _, state := range rts.states {
			if state.blockStore.Height() < lastHeight {
				return false
			}
		}
		return true
	}, 45*time.Second, 50*time.Millisecond, "every node commits %d blocks", lastHeight)

	for height := int64(1); height < lastHeight; height++ {
		var want *types.Commit
		for _, state := range rts.states {
			block := state.blockStore.LoadBlock(height)
			commit := state.blockStore.LoadBlockCommit(height)
			require.NotNil(t, block)
			require.NotNil(t, commit)
			require.Len(t, commit.Beacon, types.SignatureSize)
			stateData := state.GetStateData()
			require.NoError(t, commit.VerifyRandomBeacon(stateData.state.ChainID, stateData.Validators, &block.Header))
			require.False(t, bytes.Equal(block.AppHash, commit.AppHash))
			if want == nil {
				want = commit
				continue
			}
			require.Equal(t, want.Beacon, commit.Beacon, "height %d", height)
			require.EqualValues(t, want.AppHash, commit.AppHash, "height %d", height)
			require.True(t, want.BlockID.Equals(commit.BlockID), "height %d", height)
		}
	}
}

// replayRandomnessApp records the calls the replayer makes and answers ApplyBlockRandomness with appHash.
type replayRandomnessApp struct {
	abci.BaseApplication
	block   *types.Block
	appHash []byte
	calls   []string
}

func (a *replayRandomnessApp) ProcessProposal(_ context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	a.calls = append(a.calls, "process")
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT, AppHash: a.block.AppHash,
		TxResults: factory.ExecTxResults(types.NewTxs(req.Txs))}, nil
}

func (a *replayRandomnessApp) ApplyBlockRandomness(_ context.Context, _ *abci.RequestApplyBlockRandomness) (*abci.ResponseApplyBlockRandomness, error) {
	a.calls = append(a.calls, "randomness")
	return &abci.ResponseApplyBlockRandomness{AppHash: a.appHash}, nil
}

func (a *replayRandomnessApp) VerifyVoteExtension(_ context.Context, _ *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	a.calls = append(a.calls, "verify")
	return &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_ACCEPT}, nil
}

func (a *replayRandomnessApp) FinalizeBlock(_ context.Context, _ *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	a.calls = append(a.calls, "finalize")
	return &abci.ResponseFinalizeBlock{}, nil
}

// TestReplayBlockRandomBeacon checks that the handshake replay of a block committed at consensus version 2
// applies the commit's beacon after ProcessProposal and before the commit's extensions are checked and the
// block is finalized, reports the commit's app hash as the one the application committed, and stops
// before FinalizeBlock when the application reaches another app hash.
func TestReplayBlockRandomBeacon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	params := factory.ConsensusParams(func(cp *types.ConsensusParams) {
		cp.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
	})
	state, _ := makeGenesisState(ctx, t, nil, genesisStateArgs{Params: params, Validators: 1})
	stateStore := sm.NewStore(dbm.NewMemDB())
	require.NoError(t, stateStore.Save(state))
	block, err := sf.MakeBlock(state, state.InitialHeight, &types.Commit{}, kvstore.ProtocolVersion)
	require.NoError(t, err)

	finalAppHash := crypto.Checksum([]byte("final app hash"))
	blockID := block.BlockID(nil)
	blockID.StateID = block.Header.StateIDWithAppHash(finalAppHash).Hash()
	commit := &types.Commit{
		Height:  block.Height,
		BlockID: blockID,
		Beacon:  crypto.CRandBytes(types.SignatureSize),
		AppHash: finalAppHash,
	}

	replay := func(appHash []byte) (*replayRandomnessApp, []byte, error) {
		app := &replayRandomnessApp{block: block, appHash: appHash}
		client := abciclient.NewLocalClient(log.NewNopLogger(), app)
		exec := sm.NewBlockExecutor(stateStore, client, nil, nil, nil, nil)
		replayer := NewBlockReplayer(client, stateStore, nil, nil, nil, exec)
		_, committed, err := replayer.replayBlock(ctx, block, commit, state, block.Height)
		return app, committed, err
	}

	app, committed, err := replay(finalAppHash)
	require.NoError(t, err)
	require.Equal(t, []string{"process", "randomness", "verify", "finalize"}, app.calls)
	require.EqualValues(t, finalAppHash, committed)

	app, _, err = replay(crypto.Checksum([]byte("diverged")))
	require.ErrorIs(t, err, sm.ErrAppHashMismatch)
	require.Equal(t, []string{"process", "randomness"}, app.calls)
}
