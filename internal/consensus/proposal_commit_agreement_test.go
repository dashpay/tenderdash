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

// signedProposalFrom builds a proposal for blockID signed by the validator that
// is entitled to propose at the round, so it clears every check except the ones
// under test.
func signedProposalFrom(
	ctx context.Context,
	t *testing.T,
	n staleProposalNode,
	round int32,
	coreChainLockedHeight uint32,
	blockID types.BlockID,
) *types.Proposal {
	t.Helper()

	stateData := n.node.GetStateData()
	proposer, err := stateData.ProposerSelector.GetProposer(n.block.Height, round)
	require.NoError(t, err)

	var key types.PrivValidator
	for _, pv := range n.privVals {
		proTxHash, err := pv.GetProTxHash(ctx)
		require.NoError(t, err)
		if proTxHash.Equal(proposer.ProTxHash) {
			key = pv
		}
	}
	require.NotNil(t, key, "the entitled proposer's key must be among the validators")

	proposal := types.NewProposal(
		n.block.Height, coreChainLockedHeight, round, -1, blockID, n.block.Header.Time)
	proto := proposal.ToProto()
	vals := stateData.Validators
	_, err = key.SignProposal(ctx, stateData.state.ChainID, vals.QuorumType, vals.QuorumHash, proto)
	require.NoError(t, err)
	proposal.Signature = proto.Signature
	return proposal
}

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

	n := newStaleProposalNode(ctx, t, cfg, 64, 0)
	require.Greater(t, n.parts.Total(), uint32(1), "the commit must be able to arrive before the last part")
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

	n := newStaleProposalNode(ctx, t, cfg, 64, 0)
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
