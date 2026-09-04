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

// TestEquivocatedProposalCannotStrandAParkedCommit pins the last route into the
// dashpay/tenderdash#1414 stall. Once a commit is parked its block is fixed, and
// the round state is collecting that block's parts. A proposer that equivocates
// — a second proposal, for another block, in the round the commit names — would
// otherwise be installed, and the committed block's last part is then rejected
// against its core chain locked height. A part set completes exactly once, and
// after EnterNewRound nils it nothing rebuilds it, so the height is lost with
// blocksync the only exit. A proposal that disagrees with a commit we have
// already verified cannot be acted on and must be refused.
func TestEquivocatedProposalCannotStrandAParkedCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()

	received := types.NewPartSetFromHeader(n.commit.BlockID.PartSetHeader)
	added, err := received.AddPart(n.parts.GetPart(0))
	require.NoError(t, err)
	require.True(t, added)
	stateData.ProposalBlockParts = received
	stateData.updateRoundStep(n.commit.Round, cstypes.RoundStepPrevote)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
	require.NoError(t, n.node.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))
	require.NotNil(t, stateData.Commit, "the commit must be parked while its block is still arriving")
	require.Nil(t, stateData.Proposal, "the window the equivocation aims at: no proposal held")

	// The entitled proposer equivocates, naming another block and a core chain
	// locked height the committed block disagrees with.
	evil := signedProposalFrom(ctx, t, n, n.commit.Round, n.block.CoreChainLockedHeight+7, factory.MakeBlockID())
	require.NoError(t, n.node.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
		Msg: &ProposalMessage{Proposal: evil}, PeerID: n.peerID, ReceiveTime: tmtime.Now()}))
	assert.Nil(t, stateData.Proposal,
		"a proposal naming a block other than the one the parked commit fixed must be refused")

	for i := 1; i < int(n.parts.Total()); i++ {
		msg := &BlockPartMessage{Height: n.block.Height, Round: n.commit.Round, Part: n.parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: n.peerID})
		require.NoError(t,
			n.node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: n.peerID}, &stateData),
			"block part %d of %d", i, n.parts.Total())
	}
	assert.Equal(t, int64(2), stateData.Height,
		"the committed block must still be applied; an equivocation must not cost the height")
}

// The same rule must not refuse the proposal the node is waiting for: a parked
// commit and a proposal that name the same block agree, and the proposal carries
// the receive time its timeliness is measured from.
func TestProposalAgreeingWithParkedCommitIsAccepted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()

	received := types.NewPartSetFromHeader(n.commit.BlockID.PartSetHeader)
	added, err := received.AddPart(n.parts.GetPart(0))
	require.NoError(t, err)
	require.True(t, added)
	stateData.ProposalBlockParts = received
	stateData.updateRoundStep(n.commit.Round, cstypes.RoundStepPrevote)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
	require.NoError(t, n.node.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))
	require.NotNil(t, stateData.Commit)

	honest := signedProposalFrom(ctx, t, n, n.commit.Round, n.block.CoreChainLockedHeight, n.commit.BlockID)
	receiveTime := tmtime.Now()
	require.NoError(t, n.node.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
		Msg: &ProposalMessage{Proposal: honest}, PeerID: n.peerID, ReceiveTime: receiveTime}))

	require.NotNil(t, stateData.Proposal, "the proposal for the committed block is the one we are waiting for")
	assert.True(t, stateData.Proposal.BlockID.Equals(n.commit.BlockID))
	assert.Equal(t, receiveTime, stateData.ProposalReceiveTime, "the receive time must survive with the proposal")
}

// TestCommitIsParkedWhileTheProposedBlockIsStillArriving pins what a held
// Proposal is and is not evidence of. It attests which block the round is
// collecting; it says nothing about whether that block has arrived. Treating it
// as a proxy for holding the block lets a commit pass readiness and then fail
// the block checks behind it, and the commit is dropped rather than parked —
// the node waits for a commit the network has already sent.
func TestCommitIsParkedWhileTheProposedBlockIsStillArriving(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()
	collectFirstPartOf(t, n, &stateData)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	stateData.Proposal = signedProposalFrom(ctx, t, n, n.commit.Round, n.block.CoreChainLockedHeight, n.commit.BlockID)
	stateData.ProposalReceiveTime = tmtime.Now()
	require.Nil(t, stateData.ProposalBlock, "the block this proposal names has not assembled yet")

	commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{n.commit}, PeerID: n.peerID})
	require.NoError(t, n.node.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: n.commit, PeerID: n.peerID}, &stateData))

	require.NotNil(t, stateData.Commit, "a verified commit must be parked until its block arrives")
	assert.NotNil(t, stateData.Proposal, "the proposal names the committed block, so it is not stale")

	for i := 1; i < int(n.parts.Total()); i++ {
		msg := &BlockPartMessage{Height: n.block.Height, Round: n.commit.Round, Part: n.parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: n.peerID})
		require.NoError(t,
			n.node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: n.peerID}, &stateData),
			"block part %d of %d", i, n.parts.Total())
	}
	assert.Equal(t, int64(2), stateData.Height, "the parked commit must be applied once its block completes")
}
