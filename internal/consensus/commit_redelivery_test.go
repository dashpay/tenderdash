package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	"github.com/dashpay/tenderdash/types"
)

const commitResendTestInterval = 2 * time.Second

// commitRedeliveryPeer is a non-validator peer at height 999, in round 0,
// served commits by this node at height 1000 through the votes-and-commit
// gossip handler; every commit sent is counted and, unless acknowledged, lost.
type commitRedeliveryPeer struct {
	ctx    context.Context
	clock  *clockwork.FakeClock
	ps     *PeerState
	voteCh *p2pmocks.Channel
	commit *types.Commit
	gossip func()
}

func newCommitRedeliveryPeer(t *testing.T) *commitRedeliveryPeer {
	t.Helper()
	logger := log.NewTestingLogger(t)
	clock := clockwork.NewFakeClock()
	ps := NewPeerState(logger, types.NodeID("peer"), WithPeerStateClock(clock))
	ps.ApplyNewRoundStepMessage(&NewRoundStepMessage{Height: 999, Round: 0, Step: cstypes.RoundStepPrevote})
	voteCh := p2pmocks.NewChannel(t)
	voteCh.On("Send", mock.Anything, mock.Anything).Return(nil).Maybe()
	blockStore := &mocks.BlockStore{}
	blockStore.On("Base").Return(int64(1)).Maybe()
	gossiper := &msgGossiper{
		logger:     logger,
		ps:         ps,
		msgSender:  &p2pMsgSender{logger: logger, ps: ps, chans: channelBundle{vote: voteCh}},
		blockStore: &blockRepository{BlockStore: blockStore, logger: logger},
		optimistic: true,
		clock:      clock,
	}
	commit := &types.Commit{
		Height: 999,
		Round:  1,
		BlockID: types.BlockID{
			Hash:          tmrand.Bytes(32),
			PartSetHeader: types.PartSetHeader{Total: 3, Hash: tmrand.Bytes(32)},
		},
	}
	valSet, _ := types.RandValidatorSet(1)
	stateData := StateData{
		RoundState: cstypes.RoundState{Height: 1000, LastCommit: commit},
		state:      sm.State{Validators: valSet},
	}
	ctx := context.Background()
	hd := votesAndCommitGossipHandler(ps, blockStore, gossiper, commitResendTestInterval)
	return &commitRedeliveryPeer{
		ctx:    ctx,
		clock:  clock,
		ps:     ps,
		voteCh: voteCh,
		commit: commit,
		gossip: func() { hd(ctx, stateData) },
	}
}

func (p *commitRedeliveryPeer) sent(t *testing.T) int {
	t.Helper()
	n := 0
	for _, call := range p.voteCh.Calls {
		if call.Method == "Send" {
			n++
		}
	}
	return n
}

// A commit lost after the transport took it is sent again once the resend
// interval passes without the peer acknowledging it, although the peer keeps
// announcing the round it is in, older than the commit's.
func TestCommitGossipResendsAnUnacknowledgedCommit(t *testing.T) {
	p := newCommitRedeliveryPeer(t)

	p.gossip()
	require.Equal(t, 1, p.sent(t))

	p.ps.ApplyNewRoundStepMessage(&NewRoundStepMessage{Height: 999, Round: 0, Step: cstypes.RoundStepPrecommit})
	p.clock.Advance(commitResendTestInterval - time.Millisecond)
	p.gossip()
	require.Equal(t, 1, p.sent(t), "a commit possibly still in flight is not resent")

	p.clock.Advance(time.Millisecond)
	p.gossip()
	require.Equal(t, 2, p.sent(t), "an unacknowledged commit is resent after the interval")
	p.gossip()
	require.Equal(t, 2, p.sent(t), "a resend is marked as the peer's again")
}

// Each resend doubles the wait before the next, up to a cap.
func TestCommitGossipBacksOffResendsOfAnUnacknowledgedCommit(t *testing.T) {
	p := newCommitRedeliveryPeer(t)
	p.gossip()

	want := 1
	for _, wait := range []time.Duration{1, 2, 4, 8, 8} {
		p.clock.Advance(wait*commitResendTestInterval - time.Millisecond)
		p.gossip()
		require.Equal(t, want, p.sent(t), "no resend before %d intervals", wait)
		p.clock.Advance(time.Millisecond)
		p.gossip()
		want++
		require.Equal(t, want, p.sent(t), "resend after %d intervals", wait)
	}
}

// A commit the peer acknowledges, by a HasCommit for its height, by sending
// one itself, or by moving to the next height, is not sent again.
func TestCommitGossipStopsOnceTheCommitIsAcknowledged(t *testing.T) {
	acks := map[string]func(p *commitRedeliveryPeer){
		"has commit": func(p *commitRedeliveryPeer) {
			p.ps.ApplyHasCommitMessage(&HasCommitMessage{Height: 999, Round: 1})
		},
		"has commit of another round": func(p *commitRedeliveryPeer) {
			p.ps.ApplyHasCommitMessage(&HasCommitMessage{Height: 999, Round: 0})
		},
		"commit from the peer": func(p *commitRedeliveryPeer) {
			p.ps.SetHasCommit(p.commit)
		},
		"next height": func(p *commitRedeliveryPeer) {
			p.ps.ApplyNewRoundStepMessage(&NewRoundStepMessage{Height: 1000, Round: 0, Step: cstypes.RoundStepPropose})
		},
	}
	for name, ack := range acks {
		t.Run(name, func(t *testing.T) {
			p := newCommitRedeliveryPeer(t)
			p.gossip()
			ack(p)
			p.clock.Advance(10 * commitResendTestInterval)
			p.gossip()
			require.Equal(t, 1, p.sent(t))
		})
	}
}
