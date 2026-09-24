package blocksync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
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
	exec.On("ProcessProposal", mock.Anything, block, commit.Round, initial, true, types.VerifiedCommit{}).Twice().
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
	applier := newBlockApplier(exec, store, applierWithState(initial))
	require.Error(t, applier.Apply(ctx, block, commit))
	require.Equal(t, []string{"process", "verify"}, calls)
	require.Equal(t, initial.LastBlockHeight, applier.State().LastBlockHeight)
	require.NoError(t, applier.Apply(ctx, block, commit))
	require.Equal(t, []string{"process", "verify", "process", "verify", "save", "finalize"}, calls)
}
