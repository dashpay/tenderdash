package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

// TestAddProposalBlockPartAppliesParkedCommit covers a commit that arrives before
// the last part of the block it commits, while a Proposal for a block the network
// dropped is still around: the +2/3 prevote majority that retargeted
// ProposalBlockParts to the committed block left the Proposal untouched
// (addVoteUpdateValidBlockMw). The completing part is then checked against that
// Proposal's core chain locked height and rejected, and since a part set only
// completes once, nothing re-enters the path — the parked StateData.Commit turns
// every further commit into a no-op and the node stalls at this height
// (dashpay/tenderdash#1414).
func TestAddProposalBlockPartAppliesParkedCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	// A part size small enough that the block is gossiped in several parts, so a
	// commit can arrive while parts are still missing.
	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()

	// A proposal for a block the network dropped, disagreeing with the committed
	// block on the core chain locked height the completing part is checked against.
	staleProposal := types.NewProposal(
		n.block.Height,
		n.block.CoreChainLockedHeight+1,
		0,
		-1,
		factory.MakeBlockID(),
		n.block.Time,
	)

	received := types.NewPartSetFromHeader(n.commit.BlockID.PartSetHeader)
	added, err := received.AddPart(n.parts.GetPart(0))
	require.NoError(t, err)
	require.True(t, added)

	stateData.Proposal = staleProposal
	stateData.ProposalBlockParts = received
	stateData.updateRoundStep(n.commit.Round, cstypes.RoundStepPrevote)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
	require.NoError(t, n.node.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))
	require.NotNil(t, stateData.Commit, "the commit must be kept until the block it commits arrives")
	assert.Nil(t, stateData.Proposal, "a proposal that outlived its own block must not gate the committed block")
	assert.Equal(t, uint32(1), stateData.ProposalBlockParts.Count(), "parts already collected for the committed block must be kept")

	for i := 1; i < int(n.parts.Total()); i++ {
		msg := &BlockPartMessage{Height: n.block.Height, Round: n.commit.Round, Part: n.parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: n.peerID})
		require.NoError(t,
			n.node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: n.peerID}, &stateData),
			"block part %d of %d", i, n.parts.Total())
	}

	assert.Equal(t, int64(2), stateData.Height,
		"the block the parked commit was waiting for must be applied once its last part arrives")
}
