package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/consensus"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/internal/test/metricspy"
	"github.com/dashpay/tenderdash/types"
)

func TestBlockApplierRejectsCommitExtensionsAndRetries(t *testing.T) {
	ctx := context.Background()
	vals, keys := factory.MockValidatorSet()
	initial := fakeInitialState(vals)
	state := initial.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, keys, 1)
	block, commit := blocks[0], blocks[1].LastCommit
	exec := mocks.NewExecutor(t)
	store := mocks.NewBlockStore(t)
	calls := []string{}
	exec.On("VerifyCommit", initial, mock.Anything, block.Height, commit).Twice().Return(types.VerifiedCommit{}, nil)
	exec.On("ValidateBlock", mock.Anything, initial, block, types.VerifiedCommit{}).Twice().Return(nil)
	processed := processedState(initial, block, commit.Round)
	exec.On("ProcessProposal", mock.Anything, block, commit.Round, initial, true, types.VerifiedCommit{}).Once().
		Run(func(mock.Arguments) { calls = append(calls, "process") }).Return(processed, nil)
	check := func(args mock.Arguments) {
		vote := args.Get(1).(*types.Vote)
		require.Empty(t, vote.ValidatorProTxHash)
		require.Empty(t, vote.VoteExtensions)
		require.Equal(t, commit.Height, vote.Height)
		require.Equal(t, commit.Round, vote.Round)
		require.Equal(t, commit.BlockID, vote.BlockID)
		calls = append(calls, "verify")
	}
	exec.On("VerifyVoteExtension", mock.Anything, mock.Anything).Once().Run(check).Return(errors.New("invalid vote extension"))
	exec.On("VerifyVoteExtension", mock.Anything, mock.Anything).Once().Run(check).Return(nil)
	store.On("SaveBlock", block, mock.Anything, commit).Once().Run(func(mock.Arguments) { calls = append(calls, "save") })
	exec.On("FinalizeBlock", mock.Anything, initial, processed, mock.Anything, block, commit, types.VerifiedCommit{}).Once().
		Run(func(mock.Arguments) { calls = append(calls, "finalize") }).Return(state, nil, nil)
	counter := metricspy.NewCounter()
	metrics := consensus.NopMetrics()
	metrics.CommitVerifyFailures = counter
	applier := newBlockApplier(exec, store, applierWithState(initial), applierWithMetrics(metrics))
	require.Error(t, applier.Apply(ctx, block, commit))
	require.Equal(t, float64(1), counter.Value())
	require.Equal(t, []string{"process", "verify"}, calls)
	require.Equal(t, initial.LastBlockHeight, applier.State().LastBlockHeight)
	require.NoError(t, applier.Apply(ctx, block, commit))
	require.Equal(t, float64(1), counter.Value())
	require.Equal(t, []string{"process", "verify", "verify", "save", "finalize"}, calls)
}

func TestBlockApplierInvalidatesRejectedProposal(t *testing.T) {
	for _, change := range []string{"block", "round", "state"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			vals, keys := factory.MockValidatorSet()
			initial := fakeInitialState(vals)
			state := initial.Copy()
			blocks := statefactory.MakeBlocks(ctx, t, 2, &state, keys, 1)
			block, commit := blocks[0], blocks[1].LastCommit
			exec := mocks.NewExecutor(t)
			store := mocks.NewBlockStore(t)
			exec.On("VerifyCommit", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Twice().Return(types.VerifiedCommit{}, nil)
			exec.On("ValidateBlock", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Twice().Return(nil)
			exec.On("ProcessProposal", mock.Anything, mock.Anything, mock.Anything, mock.Anything, true, mock.Anything).
				Twice().Return(processedState(initial, block, commit.Round), nil)
			exec.On("VerifyVoteExtension", mock.Anything, mock.Anything).
				Twice().Return(errors.New("invalid vote extension"))
			applier := newBlockApplier(exec, store, applierWithState(initial))
			require.ErrorIs(t, applier.Apply(ctx, block, commit), sm.ErrCommitExtensionsRejected)
			switch change {
			case "block":
				block.AppHash = append(block.AppHash.Copy(), 1)
			case "round":
				commit.Round++
			case "state":
				applier.UpdateState(initial)
			}
			require.ErrorIs(t, applier.Apply(ctx, block, commit), sm.ErrCommitExtensionsRejected)
		})
	}
}

// Tampered commits for one block at different rounds, each rejected, leave
// block sync to process the block again for a round it processed before another
// one. An application that refuses only an immediate repeat, like kvstore,
// allows that, so the genuine commit is applied instead of panicking.
func TestBlockApplierReprocessesRoundAfterAnother(t *testing.T) {
	ctx := context.Background()
	vals, keys := factory.MockValidatorSet()
	initial := fakeInitialState(vals)
	state := initial.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, keys, 1)
	block := blocks[0]
	commitAt := func(round int32) *types.Commit {
		commit := *blocks[1].LastCommit
		commit.Round = round
		return &commit
	}
	exec := mocks.NewExecutor(t)
	store := mocks.NewBlockStore(t)
	exec.On("VerifyCommit", initial, mock.Anything, block.Height, mock.Anything).Times(4).Return(types.VerifiedCommit{}, nil)
	exec.On("ValidateBlock", mock.Anything, initial, block, types.VerifiedCommit{}).Times(4).Return(nil)
	var rounds []int32
	exec.On("ProcessProposal", mock.Anything, block, mock.Anything, initial, true, types.VerifiedCommit{}).Times(3).
		Return(func(_ context.Context, b *types.Block, round int32, _ sm.State, _ bool,
			_ types.VerifiedCommit) (sm.CurrentRoundState, error) {
			if n := len(rounds); n > 0 && rounds[n-1] == round {
				return sm.CurrentRoundState{}, fmt.Errorf("duplicate ProcessProposal call at height %d, round %d", b.Height, round)
			}
			rounds = append(rounds, round)
			return processedState(initial, b, round), nil
		})
	exec.On("VerifyVoteExtension", mock.Anything, mock.Anything).Times(3).Return(errors.New("invalid vote extension"))
	exec.On("VerifyVoteExtension", mock.Anything, mock.Anything).Once().Return(nil)
	store.On("SaveBlock", block, mock.Anything, mock.Anything).Once()
	exec.On("FinalizeBlock", mock.Anything, initial, mock.Anything, mock.Anything, block, mock.Anything,
		types.VerifiedCommit{}).Once().Return(state, nil, nil)
	applier := newBlockApplier(exec, store, applierWithState(initial))

	for _, round := range []int32{1, 1, 2} {
		require.ErrorIs(t, applier.Apply(ctx, block, commitAt(round)), sm.ErrCommitExtensionsRejected)
	}
	require.NotPanics(t, func() { require.NoError(t, applier.Apply(ctx, block, commitAt(1))) })
	require.Equal(t, []int32{1, 2, 1}, rounds, "a round is processed again only after another one")
}

// processedState is what ProcessProposal returns for block at round on top of
// base, as the applier needs it to recognize the block when it is retried.
func processedState(base sm.State, block *types.Block, round int32) sm.CurrentRoundState {
	return sm.CurrentRoundState{
		Base:        base,
		Params:      sm.RoundParams{Source: sm.ProcessProposalSource, Round: round},
		Round:       round,
		AppHash:     block.AppHash.Copy(),
		ResultsHash: block.ResultsHash.Copy(),
	}
}

// acceptCommitExtensions plays an application that accepts every commit
// extension vector. Each given SaveBlock expectation must wait for that check:
// an unverified vector must never reach the store. Without saves the check is
// optional, for tests that never get as far as applying a block.
func acceptCommitExtensions(exec *mocks.Executor, saves ...*mock.Call) *mock.Call {
	verify := exec.On("VerifyVoteExtension", mock.Anything, mock.MatchedBy(func(vote *types.Vote) bool {
		return len(vote.ValidatorProTxHash) == 0
	})).Return(nil)
	if len(saves) == 0 {
		return verify.Maybe()
	}
	for _, save := range saves {
		save.NotBefore(verify)
	}
	return verify
}
