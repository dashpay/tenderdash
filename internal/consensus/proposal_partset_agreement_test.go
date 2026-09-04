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

// TestProposalDisagreeingWithCollectedPartsIsRefused covers the route into the
// dashpay/tenderdash#1414 stall that no commit is involved in. A polka retargets
// the round state at a block, and from then on the part set can only ever
// complete into that block. A proposal naming a different one would be installed
// against a set that can never produce its block, and the block the set does
// produce is then rejected against the installed proposal's core chain locked
// height. The part set completes exactly once, so that rejection costs the height.
func TestProposalDisagreeingWithCollectedPartsIsRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()
	collectFirstPartOf(t, n, &stateData)
	require.Nil(t, stateData.Commit, "no commit: the part set is the only thing fixing the block")

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	evil := signedProposalFrom(ctx, t, n, n.commit.Round, n.block.CoreChainLockedHeight+7, factory.MakeBlockID())
	require.NoError(t, n.node.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
		Msg: &ProposalMessage{Proposal: evil}, PeerID: n.peerID, ReceiveTime: tmtime.Now()}))

	assert.Nil(t, stateData.Proposal,
		"a proposal naming a block other than the one the round is collecting must be refused")
}

// The same rule must not refuse the proposal the round is waiting for: a part set
// and a proposal that name the same block agree.
func TestProposalAgreeingWithCollectedPartsIsAccepted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()
	collectFirstPartOf(t, n, &stateData)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	honest := signedProposalFrom(ctx, t, n, n.commit.Round, n.block.CoreChainLockedHeight, n.commit.BlockID)
	receiveTime := tmtime.Now()
	require.NoError(t, n.node.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
		Msg: &ProposalMessage{Proposal: honest}, PeerID: n.peerID, ReceiveTime: receiveTime}))

	require.NotNil(t, stateData.Proposal, "the proposal for the block being collected must be installed")
	assert.True(t, stateData.Proposal.BlockID.Equals(n.commit.BlockID))
	assert.Equal(t, receiveTime, stateData.ProposalReceiveTime)
}

// TestAssembledBlockIsNotJudgedByAnUnrelatedProposal pins the check that turns a
// stale proposal into a lost height. A completed part set is judged against the
// held Proposal's core chain locked height even when that Proposal describes a
// different block, where the two headers have no reason to agree. The comparison
// is only meaningful for a proposal that names the assembled block; against any
// other it rejects a block the node asked for and cannot ask for again.
func TestAssembledBlockIsNotJudgedByAnUnrelatedProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := configSetup(t)

	n := newCommitFixture(ctx, t, cfg, multiPartBlockPartSize, 0)
	stateData := n.node.GetStateData()
	collectFirstPartOf(t, n, &stateData)

	ctx = dash.ContextWithProTxHash(ctx, n.node.privValidator.ProTxHash)
	stateData.Proposal = signedProposalFrom(
		ctx, t, n, n.commit.Round, n.block.CoreChainLockedHeight+7, factory.MakeBlockID())
	stateData.ProposalReceiveTime = tmtime.Now()

	for i := 1; i < int(n.parts.Total()); i++ {
		msg := &BlockPartMessage{Height: n.block.Height, Round: n.commit.Round, Part: n.parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: n.peerID})
		require.NoError(t,
			n.node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: n.peerID}, &stateData),
			"block part %d of %d", i, n.parts.Total())
	}

	require.NotNil(t, stateData.ProposalBlock,
		"the assembled block must be accepted; a proposal for another block does not describe it")
	assert.True(t, stateData.ProposalBlock.HashesTo(n.commit.BlockID.Hash))
}

// TestRoundStateRefusalNamesWhatFixedTheBlock keeps the two refusals apart. They
// are different observations about the sender — contradicting a verified
// threshold signature is not the same as naming a block the round cannot
// assemble — and the dispatcher swallows the error, so this is the only place
// the distinction is visible.
func TestRoundStateRefusalNamesWhatFixedTheBlock(t *testing.T) {
	const (
		height = int64(10)
		round  = int32(2)
	)
	collecting := factory.MakeBlockID()
	other := factory.MakeBlockID()
	commitFor := func(blockID types.BlockID, h int64, r int32) *types.Commit {
		return &types.Commit{Height: h, Round: r, BlockID: blockID}
	}

	testCases := []struct {
		name    string
		rs      cstypes.RoundState
		blockID types.BlockID
		wantErr error
	}{
		{
			name:    "nothing has fixed a block",
			rs:      cstypes.RoundState{},
			blockID: other,
		},
		{
			name:    "agrees with the parts being collected",
			rs:      cstypes.RoundState{ProposalBlockParts: types.NewPartSetFromHeader(collecting.PartSetHeader)},
			blockID: collecting,
		},
		{
			name:    "disagrees with the parts being collected",
			rs:      cstypes.RoundState{ProposalBlockParts: types.NewPartSetFromHeader(collecting.PartSetHeader)},
			blockID: other,
			wantErr: ErrInvalidProposalForPartSet,
		},
		{
			name:    "disagrees with a commit for this height and round",
			rs:      cstypes.RoundState{Commit: commitFor(collecting, height, round)},
			blockID: other,
			wantErr: ErrInvalidProposalForCommit,
		},
		{
			name:    "a commit for another round says nothing",
			rs:      cstypes.RoundState{Commit: commitFor(collecting, height, round+1)},
			blockID: other,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proposal := types.NewProposal(height, 1, round, -1, tc.blockID, tmtime.Now())
			require.ErrorIs(t, roundStateRefusal(proposal, &tc.rs), tc.wantErr)
		})
	}
}

// TestNonValidatorProposalIsCheckedAgainstTheCommitItself keeps the check where
// the decision is made. Outside the validator set there is no proposer public
// key, so nothing on this path verifies a signature: a commit for the same
// height and round is the whole attestation, and a proposal naming another block
// is attested by nothing at all. A caller-side precondition is not a substitute
// for the comparison on the path that accepts.
func TestNonValidatorProposalIsCheckedAgainstTheCommitItself(t *testing.T) {
	const (
		height = int64(10)
		round  = int32(2)
	)
	committed := factory.MakeBlockID()
	rs := cstypes.RoundState{Commit: &types.Commit{Height: height, Round: round, BlockID: committed}}
	p := &Proposaler{}

	agreeing := types.NewProposal(height, 1, round, -1, committed, tmtime.Now())
	require.NoError(t, p.verifyProposalForNonValidatorSet(agreeing, rs))

	disagreeing := types.NewProposal(height, 1, round, -1, factory.MakeBlockID(), tmtime.Now())
	require.ErrorIs(t, p.verifyProposalForNonValidatorSet(disagreeing, rs), ErrInvalidProposalForCommit)

	require.ErrorIs(t, p.verifyProposalForNonValidatorSet(agreeing, cstypes.RoundState{}), ErrUnableToVerifyProposal)
}
