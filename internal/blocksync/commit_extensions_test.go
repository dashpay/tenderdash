package blocksync

import (
	"context"
	"errors"
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
	exec.On("ProcessProposal", mock.Anything, block, commit.Round, initial, true, types.VerifiedCommit{}).Once().
		Run(func(mock.Arguments) { calls = append(calls, "process") }).Return(sm.CurrentRoundState{}, nil)
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
	exec.On("FinalizeBlock", mock.Anything, initial, sm.CurrentRoundState{}, mock.Anything, block, commit, types.VerifiedCommit{}).Once().
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
				Twice().Return(sm.CurrentRoundState{}, nil)
			exec.On("VerifyVoteExtension", mock.Anything, mock.Anything).
				Twice().Return(errors.New("invalid vote extension"))
			applier := newBlockApplier(exec, store, applierWithState(initial))
			require.ErrorIs(t, applier.Apply(ctx, block, commit), sm.ErrCommitExtensionsRejected)
			switch change {
			case "block":
				block.ProposedAppVersion++
			case "round":
				commit.Round++
			case "state":
				applier.UpdateState(initial)
			}
			require.ErrorIs(t, applier.Apply(ctx, block, commit), sm.ErrCommitExtensionsRejected)
		})
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
