package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/internal/eventbus"
	mpmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/internal/test/metricspy"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestBlockApplierChecksAppResponseBeforeSave(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	block, commit := blocks[0], blocks[1].LastCommit
	appHash := make([]byte, crypto.DefaultAppHashSize)
	appHash[0] = 1
	app := &inconsistentProposalApp{appHash: appHash}
	client := abciclient.NewLocalClient(log.NewNopLogger(), app)
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	stateStore := sm.NewStore(dbm.NewMemDB())
	require.NoError(t, stateStore.Save(initialState))
	executor := sm.NewBlockExecutor(stateStore, client, nil, sm.EmptyEvidencePool{}, blockStore, nil)
	applier := newBlockApplier(executor, blockStore, applierWithState(initialState))

	require.Panics(t, func() { _ = applier.Apply(ctx, block, commit) })
	require.Zero(t, blockStore.Height(), "an inconsistent app response must not advance the block store")
	require.Equal(t, initialState.LastBlockHeight, applier.State().LastBlockHeight)
	loaded, err := stateStore.Load()
	require.NoError(t, err)
	require.Equal(t, initialState.LastBlockHeight, loaded.LastBlockHeight)
}

type inconsistentProposalApp struct {
	abci.BaseApplication
	appHash []byte
}

func (app *inconsistentProposalApp) ProcessProposal(
	_ context.Context, req *abci.RequestProcessProposal,
) (*abci.ResponseProcessProposal, error) {
	return &abci.ResponseProcessProposal{
		Status:    abci.ResponseProcessProposal_ACCEPT,
		AppHash:   app.appHash,
		TxResults: factory.ExecTxResults(types.NewTxs(req.Txs)),
	}, nil
}

func TestBlockApplierApply(t *testing.T) {
	ctx := context.Background()
	mockBlockExec := mocks.NewExecutor(t)
	mockBlockStore := mocks.NewBlockStore(t)
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	blockH1 := blocks[0]
	blockH1ID := blockH1.BlockID(nil)
	commitH1 := blocks[1].LastCommit
	blockH1Parts, err := blockH1.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	testCases := []struct {
		block     *types.Block
		commit    *types.Commit
		mockFn    func()
		wantErr   string
		wantPanic bool
	}{
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockStore.On("SaveBlock", blockH1, blockH1Parts, commitH1).Once()
				mockBlockExec.
					On("VerifyCommit", initialState, blockH1ID, blockH1.Height, commitH1).
					Once().
					Return(types.VerifiedCommit{}, nil)
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1, types.VerifiedCommit{}).
					Once().
					Return(nil)
				mockBlockExec.
					On("ProcessProposal", mock.Anything, blockH1, commitH1.Round, initialState, true).
					Once().
					Return(sm.CurrentRoundState{}, nil)
				mockBlockExec.
					On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, blockH1ID, blockH1, commitH1, types.VerifiedCommit{}).
					Once().
					Return(state, nil)
			},
		},
		{
			// a commit the executor rejects stops the block before it is validated,
			// saved or applied
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockExec.
					On("VerifyCommit", initialState, blockH1ID, blockH1.Height, commitH1).
					Once().
					Return(types.VerifiedCommit{}, errors.New("bad signature"))
			},
			wantErr: "invalid a commit: bad signature",
		},
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockExec.
					On("VerifyCommit", initialState, blockH1ID, blockH1.Height, commitH1).
					Once().
					Return(types.VerifiedCommit{}, nil)
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1, types.VerifiedCommit{}).
					Once().
					Return(errors.New("invalid block"))
			},
			wantErr: "invalid block",
		},
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockStore.On("SaveBlock", blockH1, blockH1Parts, commitH1).Once()
				mockBlockExec.
					On("VerifyCommit", initialState, blockH1ID, blockH1.Height, commitH1).
					Once().
					Return(types.VerifiedCommit{}, nil)
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1, types.VerifiedCommit{}).
					Once().
					Return(nil)
				mockBlockExec.
					On("ProcessProposal", mock.Anything, blockH1, commitH1.Round, initialState, true).
					Once().
					Return(sm.CurrentRoundState{}, nil)
				mockBlockExec.
					On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, blockH1ID, blockH1, commitH1, types.VerifiedCommit{}).
					Once().
					Return(state, errors.New("eeeeeeeee"))
			},
			wantPanic: true,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			applier := newBlockApplier(mockBlockExec, mockBlockStore, applierWithState(initialState))
			if tc.mockFn != nil {
				tc.mockFn()
			}
			fn := func() {
				err := applier.Apply(ctx, tc.block, tc.commit)
				tmrequire.Error(t, tc.wantErr, err)
			}
			if tc.wantPanic {
				require.Panics(t, fn)
				return
			}
			fn()
		})
	}
}

// TestApplyStatsTakeEmpty checks that take reports nothing when no block has
// been applied, so the sync rate line does not print averages of zero samples.
func TestApplyStatsTakeEmpty(t *testing.T) {
	var stats applyStats

	_, measured := stats.take()
	require.False(t, measured)
}

// TestApplyStatsTakeAveragesAndResets checks that take returns the mean over the
// blocks since the previous call, and that it clears the counters so each caller
// sees only its own interval.
func TestApplyStatsTakeAveragesAndResets(t *testing.T) {
	var stats applyStats

	stats.add(10*time.Millisecond, 2*time.Millisecond, 30*time.Millisecond, 100*time.Millisecond)
	stats.add(20*time.Millisecond, 4*time.Millisecond, 50*time.Millisecond, 200*time.Millisecond)

	timings, measured := stats.take()
	require.True(t, measured)
	require.Equal(t, 15*time.Millisecond, timings.PartSet)
	require.Equal(t, 3*time.Millisecond, timings.Verify)
	require.Equal(t, 40*time.Millisecond, timings.Save)
	require.Equal(t, 150*time.Millisecond, timings.Exec)

	// the interval is consumed, so a second call has nothing to report
	_, measured = stats.take()
	require.False(t, measured, "take must reset the counters")

	// and counting starts over rather than resuming the old average
	stats.add(6*time.Millisecond, 6*time.Millisecond, 6*time.Millisecond, 6*time.Millisecond)
	timings, measured = stats.take()
	require.True(t, measured)
	require.Equal(t, 6*time.Millisecond, timings.PartSet)
}

// TestApplyStatsSubMillisecondPreserved guards the reason the log line reports
// durations rather than whole milliseconds: these stages are routinely
// sub-millisecond, and truncating them would report 0 before and after any
// improvement.
func TestApplyStatsSubMillisecondPreserved(t *testing.T) {
	var stats applyStats
	stats.add(300*time.Microsecond, 900*time.Microsecond, time.Millisecond, time.Millisecond)

	timings, measured := stats.take()
	require.True(t, measured)
	require.Equal(t, 300*time.Microsecond, timings.PartSet)
	require.Zero(t, timings.PartSet.Milliseconds(), "the value this test exists to protect")
}

// TestBlockApplierDoesNotSaveBlockRejectedByApp checks that a block the
// application refuses never reaches the block store. Persisting it leaves the
// store a height ahead of both the state and the app, and every later start has
// to re-process that block through an application that already rejected it once
// - the restart-proof failure of dashpay/tenderdash#1413.
func TestBlockApplierDoesNotSaveBlockRejectedByApp(t *testing.T) {
	ctx := context.Background()
	mockBlockExec := mocks.NewExecutor(t)
	// no SaveBlock expectation: the store must not be touched at all, and the
	// mock fails the test on any call it was not told to expect
	mockBlockStore := mocks.NewBlockStore(t)
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	block, commit := blocks[0], blocks[1].LastCommit

	mockBlockExec.
		On("VerifyCommit", initialState, block.BlockID(nil), block.Height, commit).
		Once().
		Return(types.VerifiedCommit{}, nil)
	mockBlockExec.
		On("ValidateBlock", mock.Anything, initialState, block, types.VerifiedCommit{}).
		Once().
		Return(nil)
	mockBlockExec.
		On("ProcessProposal", mock.Anything, block, commit.Round, initialState, true).
		Once().
		Return(sm.CurrentRoundState{}, errors.New("app rejected the block"))

	applier := newBlockApplier(mockBlockExec, mockBlockStore, applierWithState(initialState))
	require.Panics(t, func() { _ = applier.Apply(ctx, block, commit) })
}

// TestBlockApplierSavesBlockBeforeFinalize pins the order the handshake relies
// on: the block is in the store before the application commits it. The store may
// be one height ahead of the state - replayer.go replays that last block on the
// next start - but a store behind the app or the state is a case it rejects
// outright, so a crash must never be able to leave one.
func TestBlockApplierSavesBlockBeforeFinalize(t *testing.T) {
	ctx := context.Background()
	mockBlockExec := mocks.NewExecutor(t)
	mockBlockStore := mocks.NewBlockStore(t)
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	block, commit := blocks[0], blocks[1].LastCommit
	blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	var calls []string
	mockBlockExec.
		On("VerifyCommit", initialState, block.BlockID(nil), block.Height, commit).
		Once().
		Return(types.VerifiedCommit{}, nil)
	mockBlockExec.
		On("ValidateBlock", mock.Anything, initialState, block, types.VerifiedCommit{}).
		Once().
		Return(nil)
	mockBlockExec.
		On("ProcessProposal", mock.Anything, block, commit.Round, initialState, true).
		Once().
		Run(func(mock.Arguments) { calls = append(calls, "process") }).
		Return(sm.CurrentRoundState{}, nil)
	mockBlockStore.
		On("SaveBlock", block, blockParts, commit).
		Once().
		Run(func(mock.Arguments) { calls = append(calls, "save") })
	mockBlockExec.
		On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, block.BlockID(blockParts), block, commit, types.VerifiedCommit{}).
		Once().
		Run(func(mock.Arguments) { calls = append(calls, "finalize") }).
		Return(state, nil)

	applier := newBlockApplier(mockBlockExec, mockBlockStore, applierWithState(initialState))
	require.NoError(t, applier.Apply(ctx, block, commit))
	require.Equal(t, []string{"process", "save", "finalize"}, calls)
}

// TestBlockApplierRecordsStageMetrics checks that a successful apply records
// one sample per stage of the pipeline, that the verify stage is split into
// the commit signature check and block validation, and that the idle time
// between attempts excludes failed applies and is only recorded once there is
// a previous attempt to measure from.
func TestBlockApplierRecordsStageMetrics(t *testing.T) {
	ctx := context.Background()
	mockBlockExec := mocks.NewExecutor(t)
	mockBlockStore := mocks.NewBlockStore(t)
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	blockH1 := blocks[0]
	commitH1 := blocks[1].LastCommit

	mockBlockExec.On("VerifyCommit", initialState, blockH1.BlockID(nil), blockH1.Height, commitH1).Twice().Return(types.VerifiedCommit{}, nil)
	mockBlockExec.On("VerifyCommit", initialState, blockH1.BlockID(nil), blockH1.Height, new(types.Commit)).
		Once().Return(types.VerifiedCommit{}, errors.New("bad signature"))
	mockBlockStore.On("SaveBlock", blockH1, mock.Anything, commitH1).Twice()
	mockBlockExec.On("ValidateBlock", mock.Anything, mock.Anything, blockH1, types.VerifiedCommit{}).Twice().Return(nil)
	mockBlockExec.On("ProcessProposal", mock.Anything, blockH1, commitH1.Round, initialState, true).
		Twice().Return(sm.CurrentRoundState{}, nil)
	mockBlockExec.On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, mock.Anything, blockH1, commitH1, types.VerifiedCommit{}).
		Twice().Return(initialState, nil)

	hist := metricspy.NewHistogram("stage")
	m := consensus.NopMetrics()
	m.BlockSyncApplyStageDuration = hist
	applier := newBlockApplier(mockBlockExec, mockBlockStore,
		applierWithState(initialState), applierWithMetrics(m))

	require.NoError(t, applier.Apply(ctx, blockH1, commitH1))

	for _, stage := range []string{"partset", "verify_commit", "verify_block", "save", "exec"} {
		require.Len(t, hist.Samples[stage], 1, "stage %q after the first apply", stage)
		require.GreaterOrEqual(t, hist.Samples[stage][0], 0.0, "stage %q", stage)
	}
	require.Empty(t, hist.Samples["wait"], "there is no previous block to wait from on the first apply")

	// A failed attempt must advance the idle-time boundary as well, so a retry
	// does not count the failed attempt's wait and verification time again.
	failureStart := time.Now()
	require.Error(t, applier.Apply(ctx, blockH1, new(types.Commit)))
	failureEnd := time.Now()
	failureDone := applier.lastDone
	require.False(t, failureDone.Before(failureStart), "failed apply must advance lastDone")
	require.False(t, failureDone.After(failureEnd), "lastDone must be set before Apply returns")
	require.Len(t, hist.Samples["wait"], 1)

	// The retry measures only the idle time after the failed attempt.
	retryStart := time.Now()
	require.NoError(t, applier.Apply(ctx, blockH1, commitH1))
	retryEnd := time.Now()
	require.Len(t, hist.Samples["wait"], 2)
	wait := hist.Samples["wait"][1]
	require.GreaterOrEqual(t, wait, float64(retryStart.Sub(failureDone))/float64(time.Millisecond))
	require.LessOrEqual(t, wait, float64(retryEnd.Sub(failureDone))/float64(time.Millisecond))
	require.Len(t, hist.Samples["exec"], 2)
}

// TestBlockApplierVerifyFailureTimesOnlyTheCheckThatRan guards the attribution
// of a failed verify: when the commit check fails, block validation never runs
// and must not get a sample, so repeated bad commits do not skew its mean.
func TestBlockApplierVerifyFailureTimesOnlyTheCheckThatRan(t *testing.T) {
	ctx := context.Background()
	mockBlockExec := mocks.NewExecutor(t)
	mockBlockStore := mocks.NewBlockStore(t)
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	blockH1 := blocks[0]

	hist := metricspy.NewHistogram("stage")
	m := consensus.NopMetrics()
	m.BlockSyncApplyStageDuration = hist
	applier := newBlockApplier(mockBlockExec, mockBlockStore,
		applierWithState(initialState), applierWithMetrics(m))

	// A rejected commit must stop before ValidateBlock is reached.
	mockBlockExec.On("VerifyCommit", initialState, blockH1.BlockID(nil), blockH1.Height, new(types.Commit)).
		Once().Return(types.VerifiedCommit{}, errors.New("bad signature"))
	require.Error(t, applier.Apply(ctx, blockH1, new(types.Commit)))

	require.Len(t, hist.Samples["partset"], 1)
	require.Len(t, hist.Samples["verify_commit"], 1)
	require.Empty(t, hist.Samples["verify_block"], "block validation did not run")
	require.Empty(t, hist.Samples["save"])
	require.Empty(t, hist.Samples["exec"])
}

// TestBlockApplierOffersTheVerifiedCommitForward checks that the verified
// commit the executor returns for a block's commit is what the applier offers
// when it validates and applies the next block, whose LastCommit is that
// commit. The first block has none to offer, a rejected commit does not replace
// it, and replacing the state discards it.
func TestBlockApplierOffersTheVerifiedCommitForward(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 3, &state, privVals, 1)
	blockH1, blockH2 := blocks[0], blocks[1]
	commitH1, commitH2 := blocks[1].LastCommit, blocks[2].LastCommit
	blockH1ID, blockH2ID := blockH1.BlockID(nil), blockH2.BlockID(nil)

	// a genuine verification, so the expectations can tell it from the zero value
	verifiedH1, err := types.VerifyCommitSignatures(initialState.Validators, initialState.ChainID,
		blockH1ID, blockH1.Height, commitH1, nil)
	require.NoError(t, err)
	require.NotEqual(t, types.VerifiedCommit{}, verifiedH1)
	none := types.VerifiedCommit{}

	// applyH1 returns an applier that has applied block 1, offering no
	// verification because there is no previous commit
	applyH1 := func(t *testing.T) (*blockApplier, *mocks.Executor) {
		blockExec := mocks.NewExecutor(t)
		blockStore := mocks.NewBlockStore(t)
		blockStore.On("SaveBlock", mock.Anything, mock.Anything, mock.Anything).Maybe()
		blockExec.On("VerifyCommit", mock.Anything, blockH1ID, blockH1.Height, commitH1).Once().Return(verifiedH1, nil)
		blockExec.On("ValidateBlock", mock.Anything, mock.Anything, blockH1, none).Once().Return(nil)
		blockExec.On("ProcessProposal", mock.Anything, blockH1, commitH1.Round, mock.Anything, true).
			Once().Return(sm.CurrentRoundState{}, nil)
		blockExec.On("FinalizeBlock", mock.Anything, mock.Anything, mock.Anything, blockH1ID, blockH1, commitH1, none).
			Once().Return(initialState, nil)
		applier := newBlockApplier(blockExec, blockStore, applierWithState(initialState))
		require.NoError(t, applier.Apply(ctx, blockH1, commitH1))
		return applier, blockExec
	}
	// expectH2 expects block 2 to be validated and applied offering lastCommit
	expectH2 := func(blockExec *mocks.Executor, lastCommit types.VerifiedCommit) {
		blockExec.On("VerifyCommit", mock.Anything, blockH2ID, blockH2.Height, commitH2).Once().Return(none, nil)
		blockExec.On("ValidateBlock", mock.Anything, mock.Anything, blockH2, lastCommit).Once().Return(nil)
		blockExec.On("ProcessProposal", mock.Anything, blockH2, commitH2.Round, mock.Anything, true).
			Once().Return(sm.CurrentRoundState{}, nil)
		blockExec.On("FinalizeBlock", mock.Anything, mock.Anything, mock.Anything, blockH2ID, blockH2, commitH2, lastCommit).
			Once().Return(initialState, nil)
	}

	t.Run("the next block is offered the previous commit's verification", func(t *testing.T) {
		applier, blockExec := applyH1(t)
		expectH2(blockExec, verifiedH1)
		require.NoError(t, applier.Apply(ctx, blockH2, commitH2))
	})

	t.Run("a rejected commit does not replace the verification", func(t *testing.T) {
		applier, blockExec := applyH1(t)
		bad := new(types.Commit)
		blockExec.On("VerifyCommit", mock.Anything, blockH2ID, blockH2.Height, bad).
			Once().Return(none, errors.New("bad signature"))
		require.Error(t, applier.Apply(ctx, blockH2, bad))

		expectH2(blockExec, verifiedH1)
		require.NoError(t, applier.Apply(ctx, blockH2, commitH2))
	})

	t.Run("replacing the state discards the verification", func(t *testing.T) {
		applier, blockExec := applyH1(t)
		applier.UpdateState(initialState)
		expectH2(blockExec, none)
		require.NoError(t, applier.Apply(ctx, blockH2, commitH2))
	})
}

// TestBlockApplierSkipsTheLastCommitItVerified runs consecutive blocks through a
// real executor and checks that every block after the first is validated and
// applied without threshold-verifying its LastCommit again: the applier
// verified that commit when it applied the previous block, and the proof is
// handed forward with it. Every other test either mocks the executor or offers
// the proof by hand, so a broken hand-over would leave them all green while
// block sync silently verified each commit twice more.
func TestBlockApplierSkipsTheLastCommitItVerified(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	stateStore := sm.NewStore(dbm.NewMemDB())
	require.NoError(t, stateStore.Save(state))
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	app := proxy.New(abciclient.NewLocalClient(logger, &abci.BaseApplication{}), logger, proxy.NopMetrics())
	require.NoError(t, app.Start(ctx))
	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(nil)

	skipped := metricspy.NewCounter()
	execMetrics := sm.NopMetrics()
	execMetrics.LastCommitVerificationSkipped = skipped
	blockExec := sm.NewBlockExecutor(stateStore, app, mp, sm.EmptyEvidencePool{}, blockStore, eventBus,
		sm.BockExecWithMetrics(execMetrics))
	applier := newBlockApplier(blockExec, blockStore, applierWithState(state))

	// Height 1 has no LastCommit to verify. Every later height skips it twice:
	// in the validation before the block is saved, and in the validation
	// FinalizeBlock runs against the round state. (ApplyBlock's own ValidateBlock
	// is answered from the executor's per-block cache.)
	commit := types.NewCommit(0, 0, types.BlockID{}, nil, nil)
	for _, step := range []struct {
		height      int64
		wantSkipped float64
	}{{1, 0}, {2, 2}, {3, 4}} {
		block, _, _, seenCommit := makeNextBlock(ctx, t, applier.State(), privVals[0], step.height, commit)
		require.NoError(t, applier.Apply(ctx, block, seenCommit))
		require.Equal(t, step.wantSkipped, skipped.Value(),
			"LastCommit threshold verifications skipped after applying height %d", step.height)
		commit = seenCommit
	}
}
