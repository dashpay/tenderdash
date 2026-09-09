package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// forgeField returns blockID with one byte of the named field flipped, leaving
// every other field honest. That is the shape that matters: a BlockID whose Hash
// and PartSetHeader are genuine passes every hash comparison on the way to a
// signature, so only a comparison against the block itself can catch it.
func forgeField(t *testing.T, blockID types.BlockID, field string) types.BlockID {
	t.Helper()

	forged := blockID
	switch field {
	case "state_id":
		sid := make(tmbytes.HexBytes, len(blockID.StateID))
		copy(sid, blockID.StateID)
		require.NotEmpty(t, sid, "the block must have a StateID to forge")
		sid[0] ^= 0xFF
		forged.StateID = sid
	case "hash":
		h := make(tmbytes.HexBytes, len(blockID.Hash))
		copy(h, blockID.Hash)
		require.NotEmpty(t, h, "the block must have a Hash to forge")
		h[0] ^= 0xFF
		forged.Hash = h
	default:
		t.Fatalf("unknown field %q", field)
	}
	require.False(t, forged.Equals(blockID), "the forgery must actually differ")
	return forged
}

// TestRoundStateBlockIDPrefersTheBlockItHolds pins which of the two answers
// reaches a vote signature. A Proposal's BlockID is proposer-signed but describes
// a block nobody had seen when it was signed; the block and its parts are the
// block itself. Returning the claim lets a proposer put a value of its choosing
// into every honest node's prevote, and a commit built from those votes names a
// block that the next height's LastCommit can never match.
func TestRoundStateBlockIDPrefersTheBlockItHolds(t *testing.T) {
	block := &types.Block{
		Header:     *factory.MakeHeader(t, &types.Header{Height: 10}),
		LastCommit: &types.Commit{},
	}
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	honest := block.BlockID(parts)
	require.NotEmpty(t, honest.StateID)

	for _, field := range []string{"state_id", "hash"} {
		t.Run("forged "+field, func(t *testing.T) {
			forged := forgeField(t, honest, field)
			rs := cstypes.RoundState{
				Height:             10,
				Round:              0,
				Proposal:           types.NewProposal(10, 1, 0, -1, forged, tmtime.Now()),
				ProposalBlock:      block,
				ProposalBlockParts: parts,
			}
			assert.True(t, rs.BlockID().Equals(honest),
				"the block ID that reaches a signature must describe the block we hold")
			assert.False(t, rs.BlockID().Equals(forged),
				"a proposer's claim must not reach a signature unchecked")
		})
	}

	t.Run("honest proposal is unaffected", func(t *testing.T) {
		rs := cstypes.RoundState{
			Height:             10,
			Round:              0,
			Proposal:           types.NewProposal(10, 1, 0, -1, honest, tmtime.Now()),
			ProposalBlock:      block,
			ProposalBlockParts: parts,
		}
		assert.True(t, rs.BlockID().Equals(honest))
	})

	t.Run("no block held falls back to the proposal", func(t *testing.T) {
		rs := cstypes.RoundState{
			Height:   10,
			Round:    0,
			Proposal: types.NewProposal(10, 1, 0, -1, honest, tmtime.Now()),
		}
		assert.True(t, rs.BlockID().Equals(honest),
			"with no block to derive from, the proposal is all there is")
	})
}

// TestCompletedBlockRepairsUnsignedProposalStateID covers the legacy proposal
// format, where StateID is not in the proposer sign bytes. A relay may alter it,
// so the completed block is the authority and repairs the proposal slot.
func TestCompletedBlockRepairsUnsignedProposalStateID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := configSetup(t)

	cs, vss := makeState(ctx, t, makeStateArgs{config: config, validators: 4})
	stateData := cs.GetStateData()
	height, round := stateData.Height, stateData.Round

	proposal, block := decideProposal(ctx, t, cs, vss[0], height, round)
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	honest := block.BlockID(parts)
	require.True(t, proposal.BlockID.Equals(honest),
		"an honest proposer derives the block ID from the block and its parts")

	proposal.BlockID = forgeField(t, honest, "state_id")
	receivedProposal := proposal
	forged := proposal.BlockID.Copy()

	stateData = cs.GetStateData()
	stateData.Proposal = proposal
	stateData.ProposalReceiveTime = tmtime.Now()
	stateData.ProposalBlockParts = types.NewPartSetFromHeader(honest.PartSetHeader)
	require.NoError(t, stateData.Save())

	for i := 0; i < int(parts.Total()); i++ {
		msg := &BlockPartMessage{Height: height, Round: round, Part: parts.GetPart(i)}
		partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: "peerX"})
		require.NoError(t,
			cs.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: "peerX"}, &stateData),
			"the block itself is valid and must still be collected: part %d", i)
	}

	require.NotNil(t, stateData.ProposalBlock, "the block is the bytes; it is kept")
	require.NotNil(t, stateData.Proposal)
	assert.NotSame(t, receivedProposal, stateData.Proposal,
		"repair must publish a copy because gossip workers may retain the original proposal")
	assert.True(t, receivedProposal.BlockID.Equals(forged),
		"repair must not mutate a proposal visible to concurrent readers")
	assert.True(t, stateData.Proposal.BlockID.Equals(honest),
		"unsigned metadata must be derived from the completed block")
	assert.False(t, stateData.ProposalReceiveTime.IsZero())
	assert.True(t, stateData.isProposalComplete())
}
