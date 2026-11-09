package consensus

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// lastCommitPeerState is a peer at height 2, round 0, whose commit for
// height 1 was at round 0, with validator bit arrays allocated for height 2.
func lastCommitPeerState(t *testing.T) *PeerState {
	t.Helper()
	ps := NewPeerState(log.NewNopLogger(), "testPeerState")
	ps.PRS.Height, ps.PRS.Round, ps.PRS.LastCommitRound = 2, 0, 0
	ps.ensureVoteBitArrays(2, 3)
	return ps
}

// A precommit of the previous height, received from a peer that has already
// moved on, must not mark the same validator index as held in the peer's
// current-height precommits. Otherwise this node's current-height precommit
// of that validator is never offered to the peer.
func TestSetHasVoteLastHeightPrecommitDoesNotMarkCurrentHeight(t *testing.T) {
	ps := lastCommitPeerState(t)

	lateLastCommit := &types.Vote{Height: 1, Round: 0, Type: tmproto.PrecommitType, ValidatorIndex: 1}
	require.NoError(t, ps.SetHasVote(lateLastCommit))

	require.False(t, ps.PRS.Precommits.GetIndex(1),
		"a height-1 precommit must not mark the peer as holding the height-2 precommit of that validator")

	ours := bits.NewBitArray(3)
	ours.SetIndex(1, true)
	vote, ok := ps.PickVoteToSend(&fixedVoteSet{height: 2, round: 0, msgType: tmproto.PrecommitType, have: ours})
	require.True(t, ok, "the height-2 precommit of validator 1 must still be offered to the peer")
	require.EqualValues(t, 1, vote.ValidatorIndex)
}

// A VoteSetBits answer about the previous height's commit (sent while the peer
// was still catching up, applied after it moved on) must not overwrite the
// peer's current-height precommit bit array.
func TestApplyVoteSetBitsLastHeightPrecommitsDoNotMarkCurrentHeight(t *testing.T) {
	ps := lastCommitPeerState(t)

	peerHas := bits.NewBitArray(3)
	for i := 0; i < 3; i++ {
		peerHas.SetIndex(i, true)
	}
	ps.ApplyVoteSetBitsMessage(&VoteSetBitsMessage{
		Height: 1, Round: 0, Type: tmproto.PrecommitType, Votes: peerHas,
	}, nil)

	require.Equal(t, bits.NewBitArray(3), ps.PRS.Precommits,
		"height-1 commit bits must not be applied to the peer's height-2 precommits")
}

// NewRoundStep moving the peer to the next height still records the round of
// its commit for the height it left.
func TestApplyNewRoundStepMessageKeepsLastCommitRound(t *testing.T) {
	ps := lastCommitPeerState(t)
	ps.PRS.Round, ps.PRS.LastCommitRound = 3, -1

	ps.ApplyNewRoundStepMessage(&NewRoundStepMessage{Height: 3, Round: 0, Step: 1, LastCommitRound: 3})

	require.EqualValues(t, 3, ps.PRS.Height)
	require.EqualValues(t, 3, ps.PRS.LastCommitRound)
}
