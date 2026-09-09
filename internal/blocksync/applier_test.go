package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/internal/test/metricspy"
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
					On("ApplyBlock", mock.Anything, initialState, blockH1ID, blockH1, commitH1).
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
					On("ApplyBlock", mock.Anything, initialState, blockH1ID, blockH1, commitH1).
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

	mockBlockStore.On("SaveBlock", blockH1, mock.Anything, commitH1).Twice()
	mockBlockExec.On("ValidateBlock", mock.Anything, mock.Anything, blockH1).Twice().Return(nil)
	mockBlockExec.On("ApplyBlock", mock.Anything, mock.Anything, mock.Anything, blockH1, commitH1).
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

	// an empty commit fails the signature check before ValidateBlock is reached
	require.Error(t, applier.Apply(ctx, blockH1, new(types.Commit)))

	require.Len(t, hist.Samples["partset"], 1)
	require.Len(t, hist.Samples["verify_commit"], 1)
	require.Empty(t, hist.Samples["verify_block"], "block validation did not run")
	require.Empty(t, hist.Samples["save"])
	require.Empty(t, hist.Samples["exec"])
}
