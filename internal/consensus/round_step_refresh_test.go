package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/types"
)

// A peer learns this node's height, round and step only from NewRoundStep
// announcements, none of which is acknowledged. One lost after the channel
// accepted it — to a full router queue or the peer's state-channel allowance —
// leaves the peer gossiping for the round it last heard of. That picture must
// be corrected without a step change on this node: the step it is in may be
// waiting on exactly the votes or proposal the peer is holding back.
func TestReactorRefreshesRoundStepAfterLostAnnouncement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const refreshInterval = 20 * time.Millisecond
	cs, _ := makeState(ctx, t, makeStateArgs{})
	cs.config.PeerQueryMaj23SleepDuration = refreshInterval
	r := NewReactor(log.NewNopLogger(), cs, nil, nil, cs.eventBus, false, NopMetrics())

	announcements := make(chan *tmcons.NewRoundStep, 64)
	stateCh := p2pmocks.NewChannel(t)
	stateCh.On("Send", mock.Anything, mock.Anything).Maybe().
		Run(func(args mock.Arguments) {
			msg, ok := args.Get(1).(p2p.Envelope).Message.(*tmcons.NewRoundStep)
			if !ok {
				return
			}
			select {
			case announcements <- msg:
			default:
			}
		}).
		Return(nil)

	peerUpdate := p2p.PeerUpdate{NodeID: "peer", Status: p2p.PeerStatusUp, ProTxHash: crypto.RandProTxHash()}
	r.peerUp(ctx, peerUpdate, 3, channelBundle{state: stateCh})
	defer r.peerDown(ctx, p2p.PeerUpdate{NodeID: "peer", Status: p2p.PeerStatusDown}, channelBundle{})

	want := cs.GetRoundState()

	// Loss window: everything announced on connect and shortly after is lost.
	lossWindow := time.After(3 * refreshInterval)
	lost := 0
drain:
	for {
		select {
		case <-announcements:
			lost++
		case <-lossWindow:
			break drain
		}
	}
	require.NotZero(t, lost, "peer up must announce the round step")

	// The peer's picture of this node, built only from what arrives after the loss.
	peerView := NewPeerState(log.NewNopLogger(), types.NodeID("this-node"))
	deadline := time.After(ensureTimeout)
	for {
		select {
		case msg := <-announcements:
			m, err := MsgFromProto(msg)
			require.NoError(t, err)
			peerView.ApplyNewRoundStepMessage(m.(*NewRoundStepMessage))
			prs := peerView.GetRoundState()
			if prs.Height == want.Height && prs.Round == want.Round && prs.Step == want.Step {
				got := cs.GetRoundState()
				require.Equal(t, want.Height, got.Height, "sender height changed during the test")
				require.Equal(t, want.Round, got.Round, "sender round changed during the test")
				require.Equal(t, want.Step, got.Step, "sender step changed during the test")
				return
			}
		case <-deadline:
			t.Fatalf("round step %d/%d/%v was never re-announced after the lost announcement; peer still sees %d/%d/%v",
				want.Height, want.Round, want.Step, peerView.PRS.Height, peerView.PRS.Round, peerView.PRS.Step)
		}
	}
}

func TestRoundStepGossipHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ps := NewPeerState(log.NewNopLogger(), "peer")
	stateData := StateData{RoundState: cstypes.RoundState{Height: 7, Round: 2, Step: cstypes.RoundStepPrevote}}
	stateCh := p2pmocks.NewChannel(t)
	stateCh.On("Send", ctx, p2p.Envelope{To: "peer", Message: stateData.RoundState.NewRoundStepMessage()}).
		Once().
		Return(nil)
	sender := &p2pMsgSender{logger: log.NewNopLogger(), ps: ps, chans: channelBundle{state: stateCh}}

	roundStepGossipHandler(log.NewNopLogger(), sender.send)(ctx, stateData)
}
