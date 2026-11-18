package consensus

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// TestVoteRedeliveryDropsBookkeepingOnHeightChange verifies that redelivery
// bookkeeping of a height the peer has left is dropped even when the new
// height starts at the same round number (round 0, the common case), rather
// than being applied to the peer's new-height vote bits.
func TestVoteRedeliveryDropsBookkeepingOnHeightChange(t *testing.T) {
	const interval = time.Second
	clock := clockwork.NewFakeClock()
	ps := NewPeerState(log.NewNopLogger(), "testPeerState",
		WithPeerStateClock(clock), WithVoteResendInterval(interval))
	ps.PRS.Height, ps.PRS.Round, ps.PRS.Step = 1, 0, 0
	ps.PRS.CatchupCommitRound, ps.PRS.ProposalPOLRound, ps.PRS.LastCommitRound = -1, -1, -1
	ps.ensureVoteBitArrays(1, 2)

	ps.ReofferUnackedVotes()
	require.NoError(t, ps.SetVoteSent(&types.Vote{Height: 1, Round: 0, Type: tmproto.PrecommitType, ValidatorIndex: 0}))

	// Peer commits height 1 in round 0 and enters height 2, round 0, without
	// acknowledging; it then reports holding precommit 0 of height 2.
	ps.ApplyNewRoundStepMessage(&NewRoundStepMessage{Height: 2, Round: 0, Step: 1, LastCommitRound: 0})
	ps.ensureVoteBitArrays(2, 2)
	require.NoError(t, ps.ApplyHasVoteMessage(&HasVoteMessage{Height: 2, Round: 0, Type: tmproto.PrecommitType, Index: 0}))

	for range 3 {
		clock.Advance(interval)
		ps.ReofferUnackedVotes()
	}
	require.True(t, ps.PRS.Precommits.GetIndex(0),
		"stale height-1 bookkeeping must not re-offer the peer's height-2 precommit")
}
