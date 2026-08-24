package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/types"
)

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
					On("ValidateBlock", mock.Anything, initialState, blockH1).
					Once().
					Return(nil)
				mockBlockExec.
					On("ProcessProposal", mock.Anything, blockH1, commitH1.Round, initialState, false).
					Once().
					Return(sm.CurrentRoundState{}, nil)
				mockBlockExec.
					On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, blockH1ID, blockH1, commitH1).
					Once().
					Return(state, nil)
			},
		},
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1).
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
					On("ValidateBlock", mock.Anything, initialState, blockH1).
					Once().
					Return(nil)
				mockBlockExec.
					On("ProcessProposal", mock.Anything, blockH1, commitH1.Round, initialState, false).
					Once().
					Return(sm.CurrentRoundState{}, nil)
				mockBlockExec.
					On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, blockH1ID, blockH1, commitH1).
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
		On("ValidateBlock", mock.Anything, initialState, block).
		Once().
		Return(nil)
	mockBlockExec.
		On("ProcessProposal", mock.Anything, block, commit.Round, initialState, false).
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
		On("ValidateBlock", mock.Anything, initialState, block).
		Once().
		Return(nil)
	mockBlockExec.
		On("ProcessProposal", mock.Anything, block, commit.Round, initialState, false).
		Once().
		Run(func(mock.Arguments) { calls = append(calls, "process") }).
		Return(sm.CurrentRoundState{}, nil)
	mockBlockStore.
		On("SaveBlock", block, blockParts, commit).
		Once().
		Run(func(mock.Arguments) { calls = append(calls, "save") })
	mockBlockExec.
		On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, block.BlockID(blockParts), block, commit).
		Once().
		Run(func(mock.Arguments) { calls = append(calls, "finalize") }).
		Return(state, nil)

	applier := newBlockApplier(mockBlockExec, mockBlockStore, applierWithState(initialState))
	require.NoError(t, applier.Apply(ctx, block, commit))
	require.Equal(t, []string{"process", "save", "finalize"}, calls)
}
