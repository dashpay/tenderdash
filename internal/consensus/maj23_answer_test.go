package consensus

import (
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// fixedVoteSet is the smallest VoteSetReader PickVoteToSend needs: which
// validators have voted, and a vote to hand back for one of them.
type fixedVoteSet struct {
	height  int64
	round   int32
	msgType tmproto.SignedMsgType
	have    *bits.BitArray
}

func (f *fixedVoteSet) GetHeight() int64         { return f.height }
func (f *fixedVoteSet) GetRound() int32          { return f.round }
func (f *fixedVoteSet) Type() byte               { return byte(f.msgType) }
func (f *fixedVoteSet) Size() int                { return f.have.Size() }
func (f *fixedVoteSet) BitArray() *bits.BitArray { return f.have.Copy() }
func (f *fixedVoteSet) IsCommit() bool           { return false }
func (f *fixedVoteSet) GetByIndex(i int32) *types.Vote {
	return &types.Vote{
		Height:         f.height,
		Round:          f.round,
		Type:           f.msgType,
		ValidatorIndex: i,
	}
}

// A vote is offered to a given peer exactly once: the gossip loop records the
// peer as having it as soon as the send returns, and PickVoteToSend only ever
// offers what that record says the peer lacks. Nothing inside a round clears
// the record again except the peer's own answer to a majority claim.
//
// So when a layer drops a vote in flight, the answer is the only thing that
// gets it resent — and the answer carries an unchanged vote count precisely
// because the vote is still missing. Suppressing on that alone deletes the
// repair: the claim is repeated, the answer never comes, and the gap survives
// until the round is abandoned.
func TestDroppedVoteIsResentOnceTheAnswerIsAllowedThrough(t *testing.T) {
	const (
		validators = 8
		missing    = int32(7)
		height     = int64(10)
		round      = int32(0)
	)
	voteType := tmproto.PrecommitType
	blockID := types.BlockID{Hash: []byte("block")}
	clock := clockwork.NewFakeClock()
	answerTTL := maj23AnswerTTLFor(0)

	// The claimant's record of what the answering peer holds: everything but
	// the one vote, so the vote it picks to send is deterministic.
	claimantView := NewPeerState(log.NewNopLogger(), "answerer")
	claimantView.PRS.Height = height
	claimantView.PRS.Round = round
	claimantView.ensureVoteBitArrays(height, validators)
	recorded := claimantView.getVoteBitArray(height, round, voteType)
	for i := 0; i < validators; i++ {
		if int32(i) != missing {
			recorded.SetIndex(i, true)
		}
	}

	// The claimant holds every vote for the block.
	claimantVotes := bits.NewBitArray(validators)
	for i := 0; i < validators; i++ {
		claimantVotes.SetIndex(i, true)
	}
	claimantVoteSet := &fixedVoteSet{height: height, round: round, msgType: voteType, have: claimantVotes}

	// The answering peer holds everything but that one vote, and keeps holding
	// everything but that one vote for as long as it never arrives.
	answererVotes := bits.NewBitArray(validators)
	for i := 0; i < validators; i++ {
		answererVotes.SetIndex(i, int32(i) != missing)
	}
	answererView := NewPeerState(log.NewNopLogger(), "claimant",
		WithPeerStateClock(clock), WithMaj23AnswerTTL(answerTTL))

	// One gossip offer, as the gossip loop makes it: pick, send, record the
	// peer as having it whatever the send did.
	offer := func() (int32, bool) {
		vote, ok := claimantView.PickVoteToSend(claimantVoteSet)
		if !ok {
			return 0, false
		}
		require.NoError(t, claimantView.SetHasVote(vote))
		return vote.ValidatorIndex, true
	}

	// One majority claim and whatever answer it draws. Reports whether the
	// claimant was told anything.
	claimAndAnswer := func() bool {
		if !answererView.ShouldAnswerVoteSetMaj23(height, round, voteType, blockID, answererVotes) {
			return false
		}
		answererView.RecordVoteSetMaj23Answer(height, round, voteType, blockID, answererVotes)
		claimantView.ApplyVoteSetBitsMessage(&VoteSetBitsMessage{
			Height:  height,
			Round:   round,
			Type:    voteType,
			BlockID: blockID,
			Votes:   answererVotes.Copy(),
		}, claimantVotes.Copy())
		return true
	}

	index, ok := offer()
	require.True(t, ok)
	require.Equal(t, missing, index, "the vote the peer lacks is the one that gets offered")
	_, ok = offer()
	require.False(t, ok, "the offer is recorded as delivered, so it is not repeated")

	// The vote is dropped in flight. The first claim repairs the record and the
	// vote is offered again.
	require.True(t, claimAndAnswer(), "the first claim must be answered")
	index, ok = offer()
	require.True(t, ok)
	require.Equal(t, missing, index)

	// Dropped again. A claim repeated straight away carries the same answer, and
	// answering it again this soon is work for nothing.
	assert.False(t, claimAndAnswer(), "a claim repeated immediately need not be answered again")
	_, ok = offer()
	require.False(t, ok)

	// But the peer is still missing the vote, and the unchanged answer is the
	// only thing that says so. Once the claim has been left unanswered long
	// enough, it must be answered again — or the vote is never resent and the
	// round is lost to a timeout.
	clock.Advance(answerTTL)
	require.True(t, claimAndAnswer(), "an unchanged answer still repairs a gap the claimant cannot see")
	index, ok = offer()
	require.True(t, ok, "the dropped vote must be offered again")
	require.Equal(t, missing, index)
}
