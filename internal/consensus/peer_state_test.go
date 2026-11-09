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
// recorded as sent is unmarked only once a full interval has elapsed since the
// rotation that followed its send — never before one interval, always by two —
// and any acknowledgement in between cancels it.
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

	// First rotation arms the clock; a vote sent right after it sits in the
	// current window.
	ps.ReofferUnackedVotes()
	require.NoError(t, ps.SetVoteSent(vote(0)))
	require.True(t, ps.PRS.Prevotes.GetIndex(0))

	// One interval later it is promoted but not yet re-offered ...
	clock.Advance(interval)
	ps.ReofferUnackedVotes()
	require.True(t, ps.PRS.Prevotes.GetIndex(0), "a vote sent within the last interval stays marked")

	// ... a vote sent now joins the next window ...
	require.NoError(t, ps.SetVoteSent(vote(1)))
	// ... and an acknowledgement withdraws vote 2 before its window is due.
	require.NoError(t, ps.SetVoteSent(vote(2)))
	require.NoError(t, ps.ApplyHasVoteMessage(&HasVoteMessage{Height: 1, Round: 1, Type: tmproto.PrevoteType, Index: 2}))

	// Before the interval nothing changes, however often it is asked.
	clock.Advance(interval - time.Nanosecond)
	for range 5 {
		ps.ReofferUnackedVotes()
	}
	require.True(t, ps.PRS.Prevotes.GetIndex(0))
	require.True(t, ps.PRS.Prevotes.GetIndex(1))

	// At the interval, vote 0 is re-offered; vote 1 waits one more rotation.
	clock.Advance(time.Nanosecond)
	ps.ReofferUnackedVotes()
	require.False(t, ps.PRS.Prevotes.GetIndex(0), "unacknowledged vote must be re-offered after a full window")
	require.True(t, ps.PRS.Prevotes.GetIndex(1))
	require.True(t, ps.PRS.Prevotes.GetIndex(2), "acknowledged vote must stay marked")

	clock.Advance(interval)
	ps.ReofferUnackedVotes()
	require.False(t, ps.PRS.Prevotes.GetIndex(1))
	require.True(t, ps.PRS.Prevotes.GetIndex(2), "acknowledged vote must stay marked")

	// A vote the peer already reports having is not tracked at all.
	require.NoError(t, ps.SetHasVote(vote(0)))
	clock.Advance(2 * interval)
	ps.ReofferUnackedVotes()
	clock.Advance(2 * interval)
	ps.ReofferUnackedVotes()
	require.True(t, ps.PRS.Prevotes.GetIndex(0), "SetHasVote records an acknowledged vote")
}
