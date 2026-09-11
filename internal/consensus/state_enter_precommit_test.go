package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// enterPrecommit repoints ProposalBlockParts when the round prevoted for a block
// we do not hold. The Proposal it leaves behind describes the block we do hold,
// which the round has just moved past, so it is stale for the same reason and by
// the same criterion as at every other retarget.
func TestEnterPrecommitDropsProposalForABlockTheRoundMovedPast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, types.BlockPartSizeBytes, 0)
	stateData := n.node.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)

	// The round prevotes for a block this node does not hold.
	chosen := factory.MakeBlockID()
	for _, vote := range n.prevote(ctx, t, chosen) {
		voteCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &VoteMessage{vote}, PeerID: n.peerID})
		require.NoError(t, n.node.ctrl.Dispatch(voteCtx, &AddVoteEvent{Vote: vote, PeerID: n.peerID}, &stateData))
	}

	// We hold a different block, complete, with the proposal that brought it.
	receiveTime := tmtime.Now()
	stateData.Proposal = types.NewProposal(
		n.block.Height, n.block.CoreChainLockedHeight, 0, -1, n.block.BlockID(n.parts), n.block.Time)
	stateData.ProposalReceiveTime = receiveTime
	stateData.ProposalBlock = n.block
	stateData.ProposalBlockParts = n.parts
	stateData.updateRoundStep(0, cstypes.RoundStepPrevote)

	precommitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
	require.NoError(t, n.node.ctrl.Dispatch(precommitCtx,
		&EnterPrecommitEvent{Height: n.block.Height, Round: 0}, &stateData))

	require.True(t, stateData.ProposalBlockParts.HasHeader(chosen.PartSetHeader),
		"the part set must be repointed at the block the round prevoted for")
	assert.Nil(t, stateData.Proposal, "a proposal for a block the round moved past must not survive the retarget")
	assert.True(t, stateData.ProposalReceiveTime.IsZero(), "the receive time belongs to the dropped proposal")
}
