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

// updateStateData is the fourth site that repoints the round state at a block
// this node does not hold, and it is reached from EnterCommitAction on every
// +2/3 precommit majority. A Proposal describing some other block must not
// survive it: kept, it rejects the committed block's own completing part on the
// core chain locked height, and a part set completes exactly once
// (dashpay/tenderdash#1414).
func TestEnterCommitDropsProposalForAnotherBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newStaleProposalNode(ctx, t, cfg, 64, 0)
	stateData := n.node.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)

	receiveTime := tmtime.Now()
	stateData.Proposal = types.NewProposal(
		n.block.Height, n.block.CoreChainLockedHeight+1, 0, -1, factory.MakeBlockID(), n.block.Time)
	stateData.ProposalReceiveTime = receiveTime
	// Precommit step, so the majority reaches EnterCommit without EnterPrecommit
	// retargeting first and doing the drop on its own.
	stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)

	committedBlockID := n.block.BlockID(n.parts)
	n.deliver(ctx, t, &stateData, n.precommit(ctx, t, committedBlockID))

	require.True(t, stateData.ProposalBlockParts.HasHeader(committedBlockID.PartSetHeader),
		"the majority must repoint the part set at the committed block")
	assert.Nil(t, stateData.Proposal, "a proposal for another block must not survive the retarget")
	assert.True(t, stateData.ProposalReceiveTime.IsZero(), "the receive time belongs to the dropped proposal")
}

// The same site must not throw away parts it has already collected for the very
// block being committed. Rebuilding the set here forces the whole block to be
// fetched again, which is the loss adoptCommit refuses twelve lines away.
func TestEnterCommitKeepsPartsAlreadyCollectedForTheCommittedBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newStaleProposalNode(ctx, t, cfg, 64, 0)
	require.Greater(t, n.parts.Total(), uint32(1), "a single-part block cannot show a partial part set")
	stateData := n.node.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)

	committedBlockID := n.block.BlockID(n.parts)
	partial := types.NewPartSetFromHeader(committedBlockID.PartSetHeader)
	added, err := partial.AddPart(n.parts.GetPart(0))
	require.NoError(t, err)
	require.True(t, added)
	stateData.ProposalBlockParts = partial
	stateData.updateRoundStep(0, cstypes.RoundStepPrecommit)

	n.deliver(ctx, t, &stateData, n.precommit(ctx, t, committedBlockID))

	require.True(t, stateData.ProposalBlockParts.HasHeader(committedBlockID.PartSetHeader),
		"the part set must still target the committed block")
	assert.Equal(t, uint32(1), stateData.ProposalBlockParts.Count(),
		"parts already collected for the committed block must not be discarded")
}
