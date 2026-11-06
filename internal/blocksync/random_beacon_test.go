package blocksync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// recoverPrevoteBeacon signs a prevote for blockID with every validator and returns the threshold
// signature recovered from them: the random beacon of the block and round.
func recoverPrevoteBeacon(
	ctx context.Context,
	t *testing.T,
	state sm.State,
	privVals []types.PrivValidator,
	height int64,
	round int32,
	blockID types.BlockID,
) []byte {
	t.Helper()
	valSet := state.Validators
	voteSet := types.NewVoteSet(state.ChainID, height, round, tmproto.PrevoteType, valSet, types.WithRandomBeacon(true))
	for _, privVal := range privVals {
		proTxHash, err := privVal.GetProTxHash(ctx)
		require.NoError(t, err)
		idx, _ := valSet.GetByProTxHash(proTxHash)
		vote := &types.Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     idx,
			Height:             height,
			Round:              round,
			Type:               tmproto.PrevoteType,
			BlockID:            blockID,
		}
		protoVote := vote.ToProto()
		require.NoError(t, privVal.SignVote(ctx, state.ChainID, valSet.QuorumType, valSet.QuorumHash, protoVote, nil))
		require.NoError(t, vote.PopulateSignsFromProto(protoVote))
		_, err = voteSet.AddVote(vote)
		require.NoError(t, err)
	}
	beacon := voteSet.ThresholdBlockSignature()
	require.Len(t, beacon, types.SignatureSize)
	return beacon
}

// TestBlockApplierRandomBeacon checks block sync at consensus version 2: the commit's beacon and app hash
// are verified against the block before anything else, the application applies the beacon after
// ProcessProposal and before the commit's extensions are checked, the block ID verified and finalized
// is the commit's, and an application reaching another app hash stops the node.
func TestBlockApplierRandomBeacon(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	initialState.ConsensusParams = *types.DefaultConsensusParams()
	initialState.ConsensusParams.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	block := blocks[0]
	blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	headerBlockID := block.BlockID(blockParts)

	signedCommit := blocks[1].LastCommit
	beacon := recoverPrevoteBeacon(ctx, t, initialState, privVals, block.Height, signedCommit.Round, headerBlockID)
	finalAppHash := tmbytes.HexBytes(crypto.Checksum(append(block.AppHash.Copy(), beacon...)))
	makeCommit := func() *types.Commit {
		commit := *signedCommit
		commit.BlockID = headerBlockID.Copy()
		commit.BlockID.StateID = block.Header.StateIDWithAppHash(finalAppHash).Hash()
		commit.Beacon = beacon
		commit.AppHash = finalAppHash
		return &commit
	}

	expectApply := func(exec *mocks.Executor, commit *types.Commit, appHash tmbytes.HexBytes) {
		exec.On("VerifyCommit", initialState, commit.BlockID, block.Height, commit).Once().
			Return(types.VerifiedCommit{}, nil)
		exec.On("ValidateBlock", mock.Anything, initialState, block, types.VerifiedCommit{}).Once().Return(nil)
		exec.On("ProcessProposal", mock.Anything, block, commit.Round, initialState, true, types.VerifiedCommit{}).Once().
			Return(sm.CurrentRoundState{}, nil)
		exec.On("ApplyBlockRandomness", mock.Anything, block, commit.Round, beacon).Once().Return(appHash, nil)
	}

	t.Run("applies the commit's randomness and finalizes the commit's block ID", func(t *testing.T) {
		exec := mocks.NewExecutor(t)
		store := mocks.NewBlockStore(t)
		commit := makeCommit()
		expectApply(exec, commit, finalAppHash)
		acceptCommitExtensions(exec, store.On("SaveBlock", block, blockParts, commit).Once())
		exec.On("FinalizeBlock", mock.Anything, initialState, sm.CurrentRoundState{}, commit.BlockID, block, commit,
			types.VerifiedCommit{}).Once().Return(state, nil, nil)

		applier := newBlockApplier(exec, store, applierWithState(initialState))
		require.NoError(t, applier.Apply(ctx, block, commit))
	})

	t.Run("refuses a commit whose beacon does not verify", func(t *testing.T) {
		exec := mocks.NewExecutor(t)
		store := mocks.NewBlockStore(t)
		commit := makeCommit()
		commit.Beacon = signedCommit.ThresholdBlockSignature
		applier := newBlockApplier(exec, store, applierWithState(initialState))
		require.ErrorIs(t, applier.Apply(ctx, block, commit), types.ErrInvalidCommitBeacon)
	})

	t.Run("stops when the application reaches another app hash", func(t *testing.T) {
		exec := mocks.NewExecutor(t)
		store := mocks.NewBlockStore(t)
		commit := makeCommit()
		expectApply(exec, commit, crypto.Checksum([]byte("diverged")))
		applier := newBlockApplier(exec, store, applierWithState(initialState))
		require.Panics(t, func() { _ = applier.Apply(ctx, block, commit) })
	})
}
