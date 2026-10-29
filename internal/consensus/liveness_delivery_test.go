package consensus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// The peer scheduler makes room in the verification budget for a message
// before handing it to the consensus goroutine, and can only do that for a
// message it can price.
func TestLivenessMessagesArePricedForTheBudget(t *testing.T) {
	for _, msg := range []Message{
		&ProposerHeartbeatMessage{Heartbeat: &types.ProposerHeartbeat{}},
		&RoundSkipVoteMessage{Vote: &types.RoundSkipVote{}},
	} {
		cost, err := budgetedMessageCost(msg)
		require.NoError(t, err)
		assert.Equal(t, baseMessageCost, cost, "%T", msg)
	}
}

// A peer's heartbeats and skip votes reach the consensus goroutine like its
// votes do. A message type without a handler would stop that goroutine, and
// one the logging middleware cannot describe would panic it.
func TestLivenessMessagesDispatch(t *testing.T) {
	type delivery struct {
		msg    Message
		peerID types.NodeID
	}
	var delivered []delivery
	wal := &countingWAL{}
	dispatcher := newMsgInfoDispatcher(nil, nil, wal, log.NewNopLogger(), nil,
		func(_ context.Context, msg Message, peerID types.NodeID) error {
			delivered = append(delivered, delivery{msg, peerID})
			return nil
		})

	heartbeat := &ProposerHeartbeatMessage{Heartbeat: &types.ProposerHeartbeat{
		Height: 3, Round: 1, Sequence: 9, ProposerProTxHash: crypto.RandProTxHash(),
	}}
	vote := &RoundSkipVoteMessage{Vote: &types.RoundSkipVote{
		Height: 3, Round: 1, ValidatorProTxHash: crypto.RandProTxHash(),
	}}
	for _, msg := range []Message{heartbeat, vote} {
		require.NoError(t, dispatcher.dispatch(context.Background(), &StateData{},
			msgInfo{Msg: msg, PeerID: "peer", ReceiveTime: time.Now()}))
	}

	assert.Equal(t, []delivery{{heartbeat, "peer"}, {vote, "peer"}}, delivered)
	assert.Zero(t, wal.writes, "neither changes state that WAL replay would have to rebuild")
}

// The reactor verifies no heartbeat or skip vote itself: that is the consensus
// goroutine's, in turn with the sender's other messages. It refuses what it can
// without a signature check, and queues the rest in the sender's lane.
func TestReactorQueuesLivenessMessagesInTheSendersLane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const peerID = types.NodeID("sender")
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		consensusParams: factory.ConsensusParams(func(c *types.ConsensusParams) {
			c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		}),
	})
	// An empty budget: a signature check on the reactor's goroutine would be
	// refused, and the message dropped instead of queued.
	budget := &tokenBudget{}
	cs.liveness.budget = budget
	cs.roundSkips.budget = budget

	stateData := cs.GetStateData()
	cs.roundSkips.track(&stateData)
	proTxHash, err := vss[1].GetProTxHash(ctx)
	require.NoError(t, err)
	_, proposer := stateData.Validators.GetByProTxHash(proTxHash)
	require.NotNil(t, proposer)
	cs.liveness.watch(livenessTarget{
		height:     stateData.Height,
		round:      stateData.Round,
		proposer:   proposer.Copy(),
		chainID:    stateData.state.ChainID,
		quorumType: stateData.Validators.QuorumType,
		quorumHash: stateData.Validators.QuorumHash,
		interval:   time.Second,
		silence:    time.Minute,
	})
	defer cs.liveness.stop()

	heartbeat := func(signer *validatorStub, named types.ProTxHash) *tmcons.ProposerHeartbeat {
		pb := &tmproto.ProposerHeartbeat{
			Height:            stateData.Height,
			Round:             stateData.Round,
			Sequence:          1,
			ProposerProTxHash: named,
			Timestamp:         tmtime.Now(),
		}
		require.NoError(t, signer.SignProposerHeartbeat(ctx, stateData.state.ChainID,
			stateData.Validators.QuorumType, stateData.Validators.QuorumHash, pb))
		return &tmcons.ProposerHeartbeat{Heartbeat: *pb}
	}
	vote := signRoundSkipVote(ctx, t, cs, vss[2], stateData.Height, stateData.Round)
	otherProTxHash, err := vss[2].GetProTxHash(ctx)
	require.NoError(t, err)

	logger := log.NewNopLogger()
	r := newStateRateLimitedReactor(ctx, clockwork.NewRealClock())
	r.state = cs
	r.peers = map[types.NodeID]*PeerState{peerID: NewPeerState(logger, peerID)}
	r.voteRateLimit = client.NewRateLimitWithBurst(ctx, 0, 1, true, logger)
	r.dataRateLimit = client.NewRateLimitWithBurst(ctx, 0, 1, true, logger)
	r.peers[peerID].SetLaneAdmission(0, cs.msgInfoQueue.admitPeer(peerID))

	inCh := make(chan p2p.Envelope, 8)
	errCh := make(chan p2p.PeerError, 8)
	stateCh := p2p.NewChannel(p2p.ConsensusStateChannel, "state", inCh, make(chan p2p.Envelope, 8), errCh)
	go r.processMsgCh(ctx, stateCh, channelBundle{state: stateCh})

	for _, msg := range []proto.Message{
		heartbeat(vss[1], proTxHash),
		&tmcons.RoundSkipVote{Vote: *vote.ToProto()},
		// Refused without a signature check: it names a validator that is not
		// the proposer. Envelopes are handled in order, so its report proves
		// the two above were handled.
		heartbeat(vss[2], otherProTxHash),
	} {
		inCh <- p2p.Envelope{From: peerID, ChannelID: p2p.ConsensusStateChannel, Message: msg}
	}

	select {
	case reported := <-errCh:
		assert.ErrorIs(t, reported.Err, errProposerHeartbeatWrongProposer)
	case <-time.After(30 * time.Second):
		t.Fatal("the heartbeat in another validator's name was not refused")
	}
	assert.Equal(t, 2, cs.msgInfoQueue.lanes.buffered(), "the heartbeat and the vote wait in the sender's lane")
	assert.Zero(t, budget.charged, "nothing was verified on the reactor's goroutine")
}

// The liveness listeners run on the consensus goroutine and on the proposer's
// heartbeat sender, so handing a message to the reactor's liveness sender must
// never wait, however far behind that sender is.
func TestQueueLivenessNeverWaits(t *testing.T) {
	r := &Reactor{logger: log.NewNopLogger(), livenessOutbox: make(chan livenessSend, 1)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.queueLiveness(livenessSend{msg: &tmcons.RoundSkipVote{}})
		// The outbox is full: this one is dropped, not waited on.
		r.queueLiveness(livenessSend{msg: &tmcons.RoundSkipVote{}})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queueLiveness waited on a full outbox")
	}
	assert.Len(t, r.livenessOutbox, 1)
}

// The liveness sender gives up on a broadcast the router cannot take promptly,
// so a peer that stops draining its queue holds back the liveness messages
// behind it by no more than that.
func TestSendLivenessIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Reactor{logger: log.NewNopLogger(), livenessOutbox: make(chan livenessSend, 4)}
	r.queueLiveness(livenessSend{msg: &tmcons.RoundSkipVote{}})
	r.queueLiveness(livenessSend{msg: &tmcons.RoundSkipCertificate{}})
	r.queueLiveness(livenessSend{msg: &tmcons.RoundSkipVote{}})

	ch := &countingBlockedChannel{}
	go r.sendLiveness(ctx, ch)
	require.Eventually(t, func() bool { return ch.sends.Load() == 3 }, 2*time.Second, time.Millisecond,
		"each send must give up on its own rather than wait out the reactor's context")
	require.NoError(t, ctx.Err())
}

// countingBlockedChannel is a p2p.Channel whose sends never complete, and
// which counts them.
type countingBlockedChannel struct {
	p2p.Channel
	sends atomic.Int32
}

func (ch *countingBlockedChannel) Send(ctx context.Context, _ p2p.Envelope) error {
	ch.sends.Add(1)
	<-ctx.Done()
	return ctx.Err()
}
