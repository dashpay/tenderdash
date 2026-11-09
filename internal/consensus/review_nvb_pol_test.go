package consensus

import (
	"testing"

	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/types"
)

// A periodic NewValidBlock that reaches us before we send the peer the round's
// proposal must not stop us from learning the proposal's POL round.
func TestReviewPeriodicNewValidBlockKeepsProposalPOLRound(t *testing.T) {
	ps := NewPeerState(log.NewNopLogger(), types.NodeID("peer"))
	ps.ApplyNewRoundStepMessage(&NewRoundStepMessage{Height: 5, Round: 2, Step: cstypes.RoundStepPropose})

	parts := types.NewPartSetFromData(tmrand.Bytes(100), 64)
	// The peer's periodic NewValidBlock: it holds the proposal's parts.
	ps.ApplyNewValidBlockMessage(&NewValidBlockMessage{
		Height: 5, Round: 2, BlockPartSetHeader: parts.Header(), BlockParts: parts.BitArray(),
	})
	// Then our data gossip sends it the proposal, a re-proposal with POL round 1.
	proposal := &types.Proposal{
		Height: 5, Round: 2, POLRound: 1,
		BlockID: types.BlockID{Hash: tmrand.Bytes(32), PartSetHeader: parts.Header()},
	}
	ps.SetHasProposal(proposal)

	prs := ps.GetRoundState()
	require.True(t, prs.Proposal)
	require.EqualValues(t, 1, prs.ProposalPOLRound, "POL round lost to the periodic NewValidBlock")
}
