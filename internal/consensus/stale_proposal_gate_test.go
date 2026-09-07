package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// rebuildWithCoreChainLockedHeight returns a block that differs from src only in
// its core chain locked height. It round-trips through proto rather than copying
// the struct, because types.Block carries a mutex.
func rebuildWithCoreChainLockedHeight(t *testing.T, src *types.Block, height uint32) *types.Block {
	t.Helper()

	pb, err := src.ToProto()
	require.NoError(t, err)
	pb.Header.CoreChainLockedHeight = height

	block, err := types.BlockFromProto(pb)
	require.NoError(t, err)
	require.NoError(t, block.ValidateBasic())
	return block
}

// TestCompletingPartSetIsNotJudgedAgainstAnotherBlocksProposal covers the case a
// proposal left over from an earlier round creates: the round collects parts for
// one block while still holding a proposal that describes a different one.
//
// The core chain locked height comparison used to run whenever any proposal was
// present, so the completing part was judged against a proposal that says nothing
// about the block being assembled. The part is consumed by AddPart before that
// judgement is reached, and PartSet.AddPart returns (false, nil) for a part it
// already holds (types/part_set.go), so the completion branch is never re-entered
// and the block cannot be assembled again by any route.
func TestCompletingPartSetIsNotJudgedAgainstAnotherBlocksProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := configSetup(t)

	cs, vss := makeState(ctx, t, makeStateArgs{config: config, validators: 4})
	stateData := cs.GetStateData()
	height, round := stateData.Height, stateData.Round

	staleProposal, staleBlock := decideProposal(ctx, t, cs, vss[0], height, round)

	// The block the round is actually collecting: same in every respect except
	// the field the stale proposal is about to be compared against.
	collected := rebuildWithCoreChainLockedHeight(t, staleBlock, staleBlock.CoreChainLockedHeight+1)
	parts, err := collected.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	require.NotEqual(t, staleProposal.CoreChainLockedHeight, collected.CoreChainLockedHeight,
		"fixture: the stale proposal must disagree, or the old code would accept the block anyway")
	require.False(t, parts.HasHeader(staleProposal.BlockID.PartSetHeader),
		"fixture: the proposal must describe other bytes, or this cannot tell the gate from its absence")

	// These two blocks differ only in their core chain locked height, and that
	// field reaches Header.Hash() through cdcEncode, which has no uint32 case and
	// returns an empty leaf for every value. So they share a block hash: a hash
	// comparison cannot separate them, and the part set header -- a root over the
	// serialized block -- is what the gate has to ask.

	stateData = cs.GetStateData()
	stateData.Proposal = staleProposal
	stateData.ProposalReceiveTime = tmtime.Now()
	stateData.ProposalBlockParts = types.NewPartSetFromHeader(parts.Header())
	require.NoError(t, stateData.Save())

	for i := 0; i < int(parts.Total()); i++ {
		msg := &BlockPartMessage{Height: height, Round: round, Part: parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: "peerX"})
		require.NoError(t,
			cs.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: "peerX"}, &stateData),
			"a part of the block being collected must be accepted: part %d", i)
	}

	require.NotNil(t, stateData.ProposalBlock,
		"the assembled block must be kept: the completing part is spent and cannot be replayed")
	assert.True(t, stateData.ProposalBlock.HashesTo(collected.Hash()),
		"the block kept must be the one the parts carried")
	assert.Nil(t, stateData.Proposal,
		"a proposal describing a different block must not survive the block that contradicts it")
}

// TestProposalDisagreeingAboutChainLockDoesNotCostTheBlock covers a proposal
// that names the block being assembled but disagrees with it about the core
// chain locked height.
//
// Two very different things produce that: a proposer that built the proposal
// wrongly, and any peer that raised the field while relaying, since
// CanonicalizeProposal omits the dash fields from what the proposer signs and
// state_proposaler.go enforces only a lower bound. This code cannot tell them
// apart and deliberately does not try -- one arrangement covers both because
// the handling does not depend on the cause. That is the property being bought:
// the outcome is safe without the diagnosis being complete.
//
// The block survives either way. It is authenticated by the part set header,
// the completing part is spent by the time the comparison runs, and dropping
// the proposal is what lets an honest copy be accepted afterwards.
func TestProposalDisagreeingAboutChainLockDoesNotCostTheBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := configSetup(t)

	cs, vss := makeState(ctx, t, makeStateArgs{config: config, validators: 4})
	stateData := cs.GetStateData()
	height, round := stateData.Height, stateData.Round

	proposal, block := decideProposal(ctx, t, cs, vss[0], height, round)
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	require.True(t, parts.HasHeader(proposal.BlockID.PartSetHeader),
		"fixture: the proposal must describe these bytes, or this repeats the staleness case")

	proposal.CoreChainLockedHeight = block.CoreChainLockedHeight + 1

	stateData = cs.GetStateData()
	stateData.Proposal = proposal
	stateData.ProposalReceiveTime = tmtime.Now()
	stateData.ProposalBlockParts = types.NewPartSetFromHeader(parts.Header())
	require.NoError(t, stateData.Save())

	for i := 0; i < int(parts.Total()); i++ {
		msg := &BlockPartMessage{Height: height, Round: round, Part: parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: "peerX"})
		require.NoError(t,
			cs.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: "peerX"}, &stateData),
			"a disagreeing proposal must not cost the round the block: part %d", i)
	}

	require.NotNil(t, stateData.ProposalBlock,
		"the block is authenticated by the part set header and must be kept")
	assert.True(t, stateData.ProposalBlock.HashesTo(block.Hash()),
		"the block kept must be the one the parts carried")
	assert.Nil(t, stateData.Proposal,
		"the proposal must be cleared, which is what lets an honest copy be accepted")
	assert.True(t, stateData.ProposalReceiveTime.IsZero(),
		"the receive time goes with the proposal it measures")
	assert.False(t, stateData.isProposalComplete(),
		"the round prevotes nil and advances rather than stalling on a spent part set")
}
