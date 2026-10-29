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
	"github.com/dashpay/tenderdash/dash"
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
		func(_ context.Context, _ *StateData, msg Message, peerID types.NodeID) error {
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

// A certificate this node recovers is applied on the spot, not queued: a full
// queue would lose it, and the collector recovers a round's certificate once.
func TestRecoveredCertificateIsAppliedAtOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		consensusParams: factory.ConsensusParams(func(c *types.ConsensusParams) {
			c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		}),
	})
	stateData := cs.GetStateData()
	cs.roundSkips.track(&stateData)
	height, round := stateData.Height, stateData.Round

	// The state is not running, so nothing would ever read a queued
	// certificate.
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	for i := 1; i <= stateData.Validators.QuorumTypeThresholdCount(); i++ {
		vote := signRoundSkipVote(ctx, t, cs, vss[i], height, round)
		require.NoError(t, cs.handleLivenessMessage(ctx, &stateData, &RoundSkipVoteMessage{Vote: vote}, "peer"))
	}

	require.NotNil(t, stateData.RoundSkip, "the certificate was applied")
	assert.Equal(t, round, stateData.RoundSkip.Round)
	assert.Equal(t, round+1, stateData.Round)
}

// livenessReactorFixture is a reactor reading a peer's state channel, over a
// CONSENSUS_VERSION_2 state that is not running, so what the reactor queues for
// the consensus goroutine stays in the peer's lane. The node watches the
// proposer vss[1] in the round it is in.
type livenessReactorFixture struct {
	cs        *State
	vss       []*validatorStub
	stateData StateData
	r         *Reactor
	inCh      chan p2p.Envelope
	errCh     chan p2p.PeerError
	// budget is empty: a signature check on the reactor's goroutine would be
	// refused, and the message dropped instead of queued.
	budget *tokenBudget
}

const livenessTestPeer = types.NodeID("sender")

func newLivenessReactorFixture(ctx context.Context, t *testing.T) *livenessReactorFixture {
	t.Helper()
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		consensusParams: factory.ConsensusParams(func(c *types.ConsensusParams) {
			c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		}),
	})
	f := &livenessReactorFixture{cs: cs, vss: vss, budget: &tokenBudget{}}
	cs.liveness.budget = f.budget
	cs.roundSkips.budget = f.budget
	f.stateData = cs.GetStateData()
	cs.roundSkips.track(&f.stateData)

	proTxHash, err := vss[1].GetProTxHash(ctx)
	require.NoError(t, err)
	_, proposer := f.stateData.Validators.GetByProTxHash(proTxHash)
	require.NotNil(t, proposer)
	cs.liveness.watch(livenessTarget{
		height:     f.stateData.Height,
		round:      f.stateData.Round,
		proposer:   proposer.Copy(),
		chainID:    f.stateData.state.ChainID,
		quorumType: f.stateData.Validators.QuorumType,
		quorumHash: f.stateData.Validators.QuorumHash,
		interval:   time.Second,
		silence:    time.Minute,
	})
	t.Cleanup(cs.liveness.stop)

	logger := log.NewNopLogger()
	f.r = newStateRateLimitedReactor(ctx, clockwork.NewRealClock())
	f.r.state = cs
	f.r.peers = map[types.NodeID]*PeerState{livenessTestPeer: NewPeerState(logger, livenessTestPeer)}
	f.r.voteRateLimit = client.NewRateLimitWithBurst(ctx, 0, 1, true, logger)
	f.r.dataRateLimit = client.NewRateLimitWithBurst(ctx, 0, 1, true, logger)
	f.r.livenessOutbox = make(chan livenessSend, 8)
	f.r.peers[livenessTestPeer].SetLaneAdmission(0, cs.msgInfoQueue.admitPeer(livenessTestPeer))

	f.inCh = make(chan p2p.Envelope, 8)
	f.errCh = make(chan p2p.PeerError, 8)
	stateCh := p2p.NewChannel(p2p.ConsensusStateChannel, "state", f.inCh, make(chan p2p.Envelope, 8), f.errCh)
	go f.r.processMsgCh(ctx, stateCh, channelBundle{state: stateCh})
	return f
}

func (f *livenessReactorFixture) send(msg proto.Message) {
	f.inCh <- p2p.Envelope{From: livenessTestPeer, ChannelID: p2p.ConsensusStateChannel, Message: msg}
}

// heartbeat returns a heartbeat for the fixture's round, signed by signer in
// the name of named.
func (f *livenessReactorFixture) heartbeat(
	ctx context.Context,
	t *testing.T,
	signer *validatorStub,
	named types.ProTxHash,
) *tmcons.ProposerHeartbeat {
	t.Helper()
	pb := &tmproto.ProposerHeartbeat{
		Height:            f.stateData.Height,
		Round:             f.stateData.Round,
		Sequence:          1,
		ProposerProTxHash: named,
		Timestamp:         tmtime.Now(),
	}
	require.NoError(t, signer.SignProposerHeartbeat(ctx, f.stateData.state.ChainID,
		f.stateData.Validators.QuorumType, f.stateData.Validators.QuorumHash, pb))
	return &tmcons.ProposerHeartbeat{Heartbeat: *pb}
}

// refused sends a heartbeat in the name of vss[2], which is not the proposer,
// and waits until the reactor reports it. Envelopes are handled in
// order, so the report proves everything sent before was handled.
func (f *livenessReactorFixture) refused(ctx context.Context, t *testing.T) {
	t.Helper()
	proTxHash, err := f.vss[2].GetProTxHash(ctx)
	require.NoError(t, err)
	f.send(f.heartbeat(ctx, t, f.vss[2], proTxHash))
	select {
	case reported := <-f.errCh:
		assert.ErrorIs(t, reported.Err, errProposerHeartbeatWrongProposer)
	case <-time.After(30 * time.Second):
		t.Fatal("the heartbeat in another validator's name was not refused")
	}
}

// The reactor verifies no heartbeat or skip vote itself: that is the consensus
// goroutine's, in turn with the sender's other messages. It refuses what it can
// without a signature check, and queues the rest in the sender's lane.
func TestReactorQueuesLivenessMessagesInTheSendersLane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newLivenessReactorFixture(ctx, t)

	proTxHash, err := f.vss[1].GetProTxHash(ctx)
	require.NoError(t, err)
	vote := signRoundSkipVote(ctx, t, f.cs, f.vss[2], f.stateData.Height, f.stateData.Round)
	f.send(f.heartbeat(ctx, t, f.vss[1], proTxHash))
	f.send(&tmcons.RoundSkipVote{Vote: *vote.ToProto()})
	f.refused(ctx, t)

	assert.Equal(t, 2, f.cs.msgInfoQueue.lanes.buffered(), "the heartbeat and the vote wait in the sender's lane")
	assert.Zero(t, f.budget.charged, "nothing was verified on the reactor's goroutine")
}

// A copy of a vote this node counted is not queued again, but once its relay
// is due again it has the counted vote go out once more (see relayAgain).
func TestReactorRelaysACountedSkipVoteAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newLivenessReactorFixture(ctx, t)
	clock := clockwork.NewFakeClock()
	f.cs.roundSkips.clock = clock

	vote := signRoundSkipVote(ctx, t, f.cs, f.vss[2], f.stateData.Height, f.stateData.Round)
	f.budget.tokens = baseMessageCost
	added, _, err := f.cs.roundSkips.add(vote, true)
	require.NoError(t, err)
	require.True(t, added)

	f.send(&tmcons.RoundSkipVote{Vote: *vote.ToProto()})
	f.refused(ctx, t)
	assert.Empty(t, f.r.livenessOutbox, "relayed as it was counted, moments ago")

	clock.Advance(f.stateData.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults().
		ProposerSilenceTimeout(f.stateData.Round) / 2)
	f.send(&tmcons.RoundSkipVote{Vote: *vote.ToProto()})
	f.refused(ctx, t)
	require.Len(t, f.r.livenessOutbox, 1)
	sent := <-f.r.livenessOutbox
	assert.Equal(t, &tmcons.RoundSkipVote{Vote: *vote.ToProto()}, sent.msg)
	assert.Zero(t, f.cs.msgInfoQueue.lanes.buffered(), "a vote already counted is not queued")
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
