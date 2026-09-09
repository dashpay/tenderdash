package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestCommitIsCheckedAgainstEveryFieldOfTheBlockID covers the route into a
// permanent halt that involves no proposal at all. A commit names a block by
// hash, part set header and state ID. The first two are compared against the
// block this round holds; the third was not, so a commit agreeing on both while
// naming a different state ID was applied against this block and then recorded
// with its own BlockID. The next height compares that record against the state
// the block produced, they disagree, and no proposer at any round can satisfy
// both -- the disagreement is sealed by a threshold signature over a finalized
// height on one side and persisted state on the other.
//
// Each field is asked separately so an operator is told which one disagreed.
func TestCommitIsCheckedAgainstEveryFieldOfTheBlockID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = dash.ContextWithProTxHash(ctx, make(types.ProTxHash, 32))

	block := &types.Block{
		Header:     *factory.MakeHeader(t, &types.Header{Height: 10}),
		LastCommit: &types.Commit{},
	}
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	honest := block.BlockID(parts)
	require.NotEmpty(t, honest.StateID)

	otherParts, err := (&types.Block{
		Header:     *factory.MakeHeader(t, &types.Header{Height: 10, CoreChainLockedHeight: 9}),
		LastCommit: &types.Commit{},
	}).MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	testCases := []struct {
		name    string
		blockID func() types.BlockID
		wantErr string
	}{
		{
			name:    "disagrees on the state ID",
			blockID: func() types.BlockID { return forgeField(t, honest, "state_id") },
			wantErr: "state ID does not match",
		},
		{
			name:    "disagrees on the hash",
			blockID: func() types.BlockID { return forgeField(t, honest, "hash") },
			wantErr: "does not hash to commit hash",
		},
		{
			name: "disagrees on the part set header",
			blockID: func() types.BlockID {
				forged := honest
				forged.PartSetHeader = otherParts.Header()
				return forged
			},
			wantErr: "ProposalBlockParts header",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := log.NewNopLogger()
			stateData := StateData{
				logger: log.NewNopLogger(),
				RoundState: cstypes.RoundState{
					Height:             10,
					ProposalBlock:      block,
					ProposalBlockParts: parts,
				},
			}
			commit := &types.Commit{Height: 10, Round: 0, BlockID: tc.blockID()}

			verified, err := verifyCommitBlock(ctx, logger, &stateData, commit)

			assert.False(t, verified, "a commit whose block ID misdescribes the block must not be applied")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr,
				"the operator must be told which field disagreed")
		})
	}
}

func TestParkedCommitChecksStateIDBeforeSavingBlock(t *testing.T) {
	chainID := t.Name()
	for _, mismatch := range []bool{false, true} {
		name := "matching state ID"
		if mismatch {
			name = "different state ID"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			css := makeConsensusState(ctx, t, configSetup(t), 2, chainID, newTickerFunc())
			privVals := make([]types.PrivValidator, 0, len(css))
			for _, node := range css {
				privVals = append(privVals, node.privValidator.PrivValidator)
			}
			proposer := css[0].GetStateData()
			node := css[1]
			stateData := node.GetStateData()
			ctx = dash.ContextWithProTxHash(ctx, node.privValidator.ProTxHash)
			block, err := sf.MakeBlock(proposer.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
			require.NoError(t, err)
			block.CoreChainLockedHeight = 1
			parts, err := block.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			blockID := block.BlockID(parts)
			if mismatch {
				blockID = forgeField(t, blockID, "state_id")
			}
			commit, err := factory.MakeCommit(ctx, blockID, block.Height, 0,
				proposer.Votes.Precommits(0), proposer.Validators, privVals)
			require.NoError(t, err)
			peerID := proposer.Validators.Proposer().NodeAddress.NodeID
			stateData.updateRoundStep(0, cstypes.RoundStepPrevote)
			commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{commit}, PeerID: peerID})
			require.NoError(t, node.ctrl.Dispatch(commitCtx,
				&TryAddCommitEvent{Commit: commit, PeerID: peerID}, &stateData))
			require.NotNil(t, stateData.Commit, "a signed commit is parked before its block arrives")
			for i := 0; i < int(parts.Total()); i++ {
				msg := &BlockPartMessage{Height: block.Height, Round: 0, Part: parts.GetPart(i)}
				partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: peerID})
				err = node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: peerID}, &stateData)
				if mismatch && i == int(parts.Total())-1 {
					require.ErrorContains(t, err, "state ID does not match")
				} else {
					require.NoError(t, err)
				}
			}
			if mismatch {
				assert.Equal(t, block.Height, stateData.Height)
				assert.Zero(t, node.blockStore.Height(), "a mismatching commit must never be persisted")
			} else {
				assert.Equal(t, block.Height+1, stateData.Height)
				assert.True(t, stateData.state.LastBlockID.Equals(blockID))
			}
		})
	}
}
