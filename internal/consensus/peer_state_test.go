package consensus

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func peerStateSetup(h, r, v int) *PeerState {
	ps := NewPeerState(log.NewNopLogger(), "testPeerState")
	ps.PRS.Height = int64(h)
	ps.PRS.Round = int32(r)
	ps.ensureVoteBitArrays(int64(h), v)
	return ps
}

func TestSetHasVote(t *testing.T) {
	ps := peerStateSetup(1, 1, 1)
	pva := ps.PRS.Prevotes.Copy()

	// nil vote should return ErrPeerStateNilVote
	err := ps.SetHasVote(nil)
	require.Equal(t, ErrPeerStateSetNilVote, err)

	// the peer giving an invalid index should returns ErrPeerStateInvalidVoteIndex
	v0 := &types.Vote{
		Height:         1,
		ValidatorIndex: -1,
		Round:          1,
		Type:           tmproto.PrevoteType,
	}

	err = ps.SetHasVote(v0)
	require.Equal(t, ErrPeerStateInvalidVoteIndex, err)

	// the peer giving an invalid index should returns ErrPeerStateInvalidVoteIndex
	v1 := &types.Vote{
		Height:         1,
		ValidatorIndex: 1,
		Round:          1,
		Type:           tmproto.PrevoteType,
	}

	err = ps.SetHasVote(v1)
	require.Equal(t, ErrPeerStateInvalidVoteIndex, err)

	// the peer giving a correct index should return nil (vote has been set)
	v2 := &types.Vote{
		Height:         1,
		ValidatorIndex: 0,
		Round:          1,
		Type:           tmproto.PrevoteType,
	}
	require.Nil(t, ps.SetHasVote(v2))

	// verify vote
	pva.SetIndex(0, true)
	require.Equal(t, pva, ps.getVoteBitArray(1, 1, tmproto.PrevoteType))

	// the vote is not in the correct height/round/voteType should return nil (ignore the vote)
	v3 := &types.Vote{
		Height:         2,
		ValidatorIndex: 0,
		Round:          1,
		Type:           tmproto.PrevoteType,
	}
	require.Nil(t, ps.SetHasVote(v3))
	// prevote bitarray has no update
	require.Equal(t, pva, ps.getVoteBitArray(1, 1, tmproto.PrevoteType))
}

func TestApplyHasVoteMessage(t *testing.T) {
	ps := peerStateSetup(1, 1, 1)
	pva := ps.PRS.Prevotes.Copy()

	// ignore the message with an invalid height
	msg := &HasVoteMessage{
		Height: 2,
	}
	require.Nil(t, ps.ApplyHasVoteMessage(msg))

	// apply a message like v2 in TestSetHasVote
	msg2 := &HasVoteMessage{
		Height: 1,
		Index:  0,
		Round:  1,
		Type:   tmproto.PrevoteType,
	}

	require.Nil(t, ps.ApplyHasVoteMessage(msg2))

	// verify vote
	pva.SetIndex(0, true)
	require.Equal(t, pva, ps.getVoteBitArray(1, 1, tmproto.PrevoteType))

	// skip test cases like v & v3 in TestSetHasVote due to the same path
}

// TestVoteRedeliveryWindows pins the cadence contract of vote redelivery: a vote
// recorded as sent is unmarked no sooner than one interval after it went out
// and no later than two, and any acknowledgement in between cancels it.
func TestVoteRedeliveryWindows(t *testing.T) {
	const interval = time.Second
	clock := clockwork.NewFakeClock()
	ps := NewPeerState(log.NewNopLogger(), "testPeerState",
		WithPeerStateClock(clock), WithVoteResendInterval(interval))
	ps.PRS.Height, ps.PRS.Round = 1, 1
	ps.ensureVoteBitArrays(1, 3)
	vote := func(idx int32) *types.Vote {
		return &types.Vote{Height: 1, Round: 1, Type: tmproto.PrevoteType, ValidatorIndex: idx}
	}

	// t=0: the first check arms the clock; t=0.5: vote 0 goes out.
	ps.ReofferUnackedVotes()
	clock.Advance(interval / 2)
	require.NoError(t, ps.SetVoteSent(vote(0)))
	require.True(t, ps.PRS.Prevotes.GetIndex(0))

	// t=1: half an interval old, vote 0 stays marked.
	clock.Advance(interval / 2)
	ps.ReofferUnackedVotes()
	require.True(t, ps.PRS.Prevotes.GetIndex(0), "a vote sent within the last interval stays marked")

	// t=1.5: votes 1 and 2 go out; vote 2 is acknowledged at once.
	clock.Advance(interval / 2)
	require.NoError(t, ps.SetVoteSent(vote(1)))
	require.NoError(t, ps.SetVoteSent(vote(2)))
	require.NoError(t, ps.ApplyHasVoteMessage(&HasVoteMessage{Height: 1, Round: 1, Type: tmproto.PrevoteType, Index: 2}))

	// Until t=2 nothing changes, however often it is asked.
	clock.Advance(interval/2 - time.Nanosecond)
	for range 5 {
		ps.ReofferUnackedVotes()
	}
	require.True(t, ps.PRS.Prevotes.GetIndex(0))
	require.True(t, ps.PRS.Prevotes.GetIndex(1))

	// t=2: vote 0 (1.5 intervals old) is re-offered; vote 1 (0.5) waits.
	clock.Advance(time.Nanosecond)
	ps.ReofferUnackedVotes()
	require.False(t, ps.PRS.Prevotes.GetIndex(0), "unacknowledged vote must be re-offered after a full interval")
	require.True(t, ps.PRS.Prevotes.GetIndex(1))
	require.True(t, ps.PRS.Prevotes.GetIndex(2), "acknowledged vote must stay marked")

	// t=3: vote 1 is re-offered.
	clock.Advance(interval)
	ps.ReofferUnackedVotes()
	require.False(t, ps.PRS.Prevotes.GetIndex(1))
	require.True(t, ps.PRS.Prevotes.GetIndex(2), "acknowledged vote must stay marked")

	// The peer's own copy of vote 0 acknowledges it for good.
	require.NoError(t, ps.SetHasVote(vote(0)))
	for range 4 {
		clock.Advance(interval)
		ps.ReofferUnackedVotes()
	}
	require.True(t, ps.PRS.Prevotes.GetIndex(0), "SetHasVote records an acknowledged vote")
}

// TestVoteRedeliveryIgnoresAckBeforeSent covers the gossip interleaving in
// which the peer's HasVote is applied between picking a vote and recording it
// as sent (the send sits between the two, unlocked). The ack must win: a
// resend could never be acknowledged, since the receiver's duplicate check
// drops it before any HasVote is published.
func TestVoteRedeliveryIgnoresAckBeforeSent(t *testing.T) {
	const interval = time.Second
	clock := clockwork.NewFakeClock()
	ps := NewPeerState(log.NewNopLogger(), "testPeerState",
		WithPeerStateClock(clock), WithVoteResendInterval(interval))
	ps.PRS.Height, ps.PRS.Round = 1, 1
	ps.ensureVoteBitArrays(1, 1)
	vote := &types.Vote{Height: 1, Round: 1, Type: tmproto.PrevoteType, ValidatorIndex: 0}

	ps.ReofferUnackedVotes()
	require.NoError(t, ps.ApplyHasVoteMessage(&HasVoteMessage{Height: 1, Round: 1, Type: tmproto.PrevoteType, Index: 0}))
	require.NoError(t, ps.SetVoteSent(vote))
	for range 3 {
		clock.Advance(interval)
		ps.ReofferUnackedVotes()
	}
	require.True(t, ps.PRS.Prevotes.GetIndex(0), "a vote acknowledged before it was recorded as sent must not be re-offered")
}

// TestVoteRedeliveryBacksOff pins the bound on a vote the peer never
// acknowledges — a lost HasVote cannot be repaired by resending, since the
// receiver drops the duplicate silently: each re-offer doubles the wait before
// the next, up to voteResendMaxBackoff, so the vote keeps being offered at a
// rate that falls off instead of every interval.
func TestVoteRedeliveryBacksOff(t *testing.T) {
	const interval = time.Second
	clock := clockwork.NewFakeClock()
	ps := NewPeerState(log.NewNopLogger(), "testPeerState",
		WithPeerStateClock(clock), WithVoteResendInterval(interval))
	ps.PRS.Height, ps.PRS.Round = 1, 1
	ps.ensureVoteBitArrays(1, 1)
	vote := &types.Vote{Height: 1, Round: 1, Type: tmproto.PrevoteType, ValidatorIndex: 0}

	// rotationsUntilReoffer resends the vote, then counts the rotations (one
	// per interval) until it is put back on offer.
	rotationsUntilReoffer := func() int {
		require.NoError(t, ps.SetVoteSent(vote))
		for n := 1; n <= 64; n++ {
			clock.Advance(interval)
			ps.ReofferUnackedVotes()
			if !ps.PRS.Prevotes.GetIndex(0) {
				return n
			}
		}
		t.Fatal("vote was never re-offered")
		return 0
	}
	ps.ReofferUnackedVotes()
	require.Equal(t, 1, rotationsUntilReoffer(), "first re-offer after one interval")
	require.Equal(t, 2, rotationsUntilReoffer(), "second re-offer doubles the wait")
	require.Equal(t, 4, rotationsUntilReoffer())
	require.Equal(t, 1<<voteResendMaxBackoff, rotationsUntilReoffer(), "wait is capped")
	require.Equal(t, 1<<voteResendMaxBackoff, rotationsUntilReoffer(), "and stays capped: the vote is never given up on")
}

// TestVoteRedeliveryVoteSetBitsAck verifies that a VoteSetBits answer counts
// as acknowledgement for the votes it covers and for those only.
func TestVoteRedeliveryVoteSetBitsAck(t *testing.T) {
	const interval = time.Second
	clock := clockwork.NewFakeClock()
	ps := NewPeerState(log.NewNopLogger(), "testPeerState",
		WithPeerStateClock(clock), WithVoteResendInterval(interval))
	ps.PRS.Height, ps.PRS.Round = 1, 1
	ps.ensureVoteBitArrays(1, 2)
	blockID := types.BlockID{Hash: []byte("block")}

	ps.ReofferUnackedVotes()
	for idx := range int32(2) {
		require.NoError(t, ps.SetVoteSent(&types.Vote{Height: 1, Round: 1, Type: tmproto.PrevoteType, ValidatorIndex: idx, BlockID: blockID}))
	}
	// The peer answers our majority claim for blockID: it holds vote 0. We
	// only hold vote 0 for that block ourselves, so vote 1 is left as it was.
	peerHas := bits.NewBitArray(2)
	peerHas.SetIndex(0, true)
	ourVotes := bits.NewBitArray(2)
	ourVotes.SetIndex(0, true)
	ps.ApplyVoteSetBitsMessage(&VoteSetBitsMessage{Height: 1, Round: 1, Type: tmproto.PrevoteType, BlockID: blockID, Votes: peerHas}, ourVotes)
	require.True(t, ps.PRS.Prevotes.GetIndex(0))
	require.True(t, ps.PRS.Prevotes.GetIndex(1))

	for range 3 {
		clock.Advance(interval)
		ps.ReofferUnackedVotes()
	}
	require.True(t, ps.PRS.Prevotes.GetIndex(0), "a vote covered by VoteSetBits is acknowledged")
	require.False(t, ps.PRS.Prevotes.GetIndex(1), "a vote not covered by VoteSetBits is still re-offered")
}
