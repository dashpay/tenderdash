package consensus

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// verificationRate is the node-wide signature-verification budget the timing
// assertions below are made against, matching the shipped default.
const verificationRate = 300

// A peer that stays connected must be served whatever the other peers send. The
// attacker holds every other lane saturated with messages it knows will fail
// verification, which is the cheapest way to keep the shared budget at zero, and
// the honest peer still gets its vote verified in time for the round.
//
// The expensive case is the one a scheduler that skipped a lane whose head it
// could not afford right now cannot handle: the honest head costs ten signature
// verifications against the attacker's one, and refusing a message costs
// nothing, so the flood holds the budget below ten indefinitely and that head is
// never admitted.
//
// What the deadlines say about the design point is worth stating plainly. Equal
// service per identity means a head of cost W waits for every other lane to
// have had W of work too, so the wait grows with the head's own cost: a prevote
// is served inside the vote timeout, while the heaviest precommit Dash
// validators produce takes about ten times as long and only fits inside the
// propose timeout. Raising the verification rate, cutting the number of lanes
// an attacker can hold, or verifying a precommit once instead of twice are what
// move that; the rotation cannot.
func TestHonestPeerVoteCompletesUnderAttackerFlood(t *testing.T) {
	// One lane per connection the node accepts, all but one adversarial.
	const attackerLanes = 67

	testCases := []struct {
		name       string
		extensions int
		// deadline is the round timeout this message has to beat for the round
		// it belongs to still to be able to finish.
		deadline time.Duration
	}{
		// A prevote, the message a round cannot finish without.
		{name: "cheap honest head", extensions: -1, deadline: types.DefaultTimeoutParams().Vote},
		// The heaviest precommit Dash validators actually produce. It is graded
		// against the propose timeout because that is what it meets: fair
		// service for a head of this cost takes longer than the vote timeout
		// allows, so under a flood of this size the round it belongs to is lost
		// and the next one carries it.
		{name: "expensive honest head", extensions: 4, deadline: types.DefaultTimeoutParams().Propose},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			clock := clockwork.NewFakeClock()
			inner := newVerificationBudget(verificationRate, withVerificationBudgetClock(clock))
			budget := &recordingVerificationBudget{inner: inner}
			cs, vss := makeState(ctx, t, makeStateArgs{
				validators: 4,
				stateOpts:  []StateOption{WithVerificationBudget(budget)},
			})
			stateData := cs.GetStateData()

			// Start with the budget already spent, so the flood is paid for out of
			// the refill rate rather than the burst and really does compete with
			// the honest peer for it.
			drainVerificationBudget(inner)
			start := clock.Now()

			// Enough per lane that no attacker runs dry before the honest head is
			// served, so nothing but the rotation limits what they get through.
			const perLane = 16
			for i := 0; i < attackerLanes; i++ {
				peerID := types.NodeID(fmt.Sprintf("attacker-%d", i))
				for j := 0; j < perLane; j++ {
					require.NoError(t, cs.msgInfoQueue.send(ctx, &CommitMessage{Commit: forgedCommit(&stateData)}, peerID))
				}
			}

			runCtx := dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
			go cs.receiveRoutine(runCtx, nil)
			stopAdvancing := advanceBudgetClock(ctx, clock, inner)
			defer stopAdvancing()

			// The honest message arrives once the flood is already being
			// verified, so it has to wait behind a turn in flight.
			require.Eventually(t, func() bool { return len(budget.charges()) > 0 },
				30*time.Second, time.Millisecond, "the flood never started")

			// The flood is only a flood if each of its messages really is as
			// cheap as it looks: an attacker that cannot forge a signature is
			// charged one verification, and it is that price which decides how
			// many turns it takes to hold the budget down. Read before the
			// honest message arrives, so only the flood's own charges are here.
			assertFloodSettlesAtBaseCost(t, budget.charges())

			honest := honestVote(ctx, t, vss[1], &stateData, tc.extensions)
			arrived := clock.Now()
			require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: honest}, "honest"))

			require.Eventually(t, func() bool { return voteAccepted(cs, honest) },
				30*time.Second, time.Millisecond,
				"the honest peer's vote was never verified while other peers flooded")
			latency := clock.Now().Sub(arrived)

			t.Logf("honest vote of cost %d completed in %s under %d saturated lanes",
				laneTurnCost(msgInfo{Msg: &VoteMessage{Vote: honest}}), latency, attackerLanes)
			require.Less(t, latency, tc.deadline,
				"the honest peer's vote must be verified within the round it belongs to")

			// Everything charged is inside the rate the budget was built for.
			// The scheduler reads the budget on its own goroutine and the
			// charges are made on another, so this bound now rests on the
			// handoff between them rather than on one goroutine doing both.
			assertWithinBudget(t, budget.charges(), clock.Now().Sub(start))
		})
	}
}

// The scheduler waits for verification budget on its own goroutine, so a
// saturated budget delays peers and nothing else. The consensus goroutine also
// drives the timeout ticker and this node's own messages: holding it there would
// make a peer flood push the node past its own round timeouts.
func TestPeerBudgetWaitLeavesTimeoutsAndLocalMessagesServed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(verificationRate, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()
	wal := &recordingWAL{}
	cs.wal = wal

	peerVote := honestVote(ctx, t, vss[1], &stateData, 4)
	localVote := honestVote(ctx, t, vss[2], &stateData, 4)

	runCtx := dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	require.NoError(t, cs.timeoutTicker.Start(runCtx))
	go cs.receiveRoutine(runCtx, nil)

	// Nothing ever advances the clock, so a peer message the budget cannot cover
	// stays uncovered for the whole test.
	drainVerificationBudget(inner)
	require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: peerVote}, "peer"))
	require.NoError(t, clock.BlockUntilContext(ctx, 1), "the peer message must be waiting for budget")

	// This node's own message is dispatched while the peer path is stuck.
	require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: localVote}, ""))
	require.Eventually(t, func() bool { return wal.contains(isLocalVoteRecord(localVote)) },
		30*time.Second, time.Millisecond,
		"this node's own message was held up behind a peer waiting for verification budget")

	// So is a round timeout.
	cs.timeoutTicker.ScheduleTimeout(timeoutInfo{
		Duration: time.Millisecond,
		Height:   stateData.Height,
		Round:    stateData.Round,
		Step:     cstypes.RoundStepPropose,
	})
	require.Eventually(t, func() bool { return wal.contains(isTimeoutRecord) },
		30*time.Second, time.Millisecond,
		"a round timeout was held up behind a peer waiting for verification budget")

	require.Empty(t, budget.charges(), "the peer message must still be waiting, or the test proves nothing")

	// A message the budget cannot cover is held before the write-ahead log,
	// which is what keeps a flood this node cannot afford from costing it a disk
	// write per message and a re-verification of the same messages on replay.
	require.False(t, wal.contains(isPeerVoteRecord(peerVote)),
		"a message still waiting for verification budget must not have been written to the log")
}

// Overflowing a lane is this node saying it cannot keep up, not the peer
// misbehaving — and under bounded lanes it is the peer sending at full rate that
// hits it first, which is what an honest peer catching us up looks like. It must
// never reach the reactor's error path, where any error becomes a peer error and
// costs the sender its connection score.
func TestSheddingAPeerMessageNeverReportsThePeer(t *testing.T) {
	testCases := []struct {
		name string
		// setup arranges for the next vote from the peer to be shed.
		setup func(t *testing.T, r *Reactor, cs *State, peerID types.NodeID)
	}{
		{
			name: "lane overflow",
			setup: func(t *testing.T, _ *Reactor, cs *State, peerID types.NodeID) {
				// One slot is already taken by the control message below.
				for i := 0; i < laneCapacity-1; i++ {
					require.NoError(t, cs.msgInfoQueue.send(context.Background(),
						&VoteMessage{Vote: testPrecommitVote(0)}, peerID))
				}
			},
		},
		{
			name: "per-peer rate limit",
			setup: func(t *testing.T, r *Reactor, _ *State, peerID types.NodeID) {
				for r.allowVoteChannelMessage(context.Background(),
					&p2p.Envelope{From: peerID, ChannelID: p2p.ConsensusVoteChannel,
						Message: testVoteMsg(tmproto.PrevoteType, testBlockID(), 0)}) { //nolint:revive
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			const peerID = types.NodeID("honest-at-full-rate")
			cs, vss := makeState(ctx, t, makeStateArgs{validators: 4})
			stateData := cs.GetStateData()
			wal := &recordingWAL{}
			cs.wal = wal

			r, inCh, errCh := newReceivingReactor(ctx, t, cs, peerID)
			vote := honestVote(ctx, t, vss[1], &stateData, 4)
			voteEnvelope := p2p.Envelope{
				From:      peerID,
				ChannelID: p2p.ConsensusVoteChannel,
				Message:   &tmcons.Vote{Vote: vote.ToProto()},
			}

			// Control: while the node has room, this very message reaches the
			// scheduler. Without it the assertions below would hold for a message
			// that never got that far.
			inCh <- voteEnvelope
			require.Eventually(t, func() bool { return cs.msgInfoQueue.lanes.buffered() == 1 },
				30*time.Second, time.Millisecond, "the vote path does not reach the scheduler")

			tc.setup(t, r, cs, peerID)
			queued := cs.msgInfoQueue.lanes.buffered()

			// The message this node cannot take, followed by one the reactor
			// really must report. Messages are handled in order, so the reported
			// one arriving first would mean the other was reported too.
			inCh <- voteEnvelope
			inCh <- p2p.Envelope{
				From:      "malformed-sender",
				ChannelID: p2p.ConsensusVoteChannel,
				Message:   &tmcons.Commit{},
			}

			select {
			case reported := <-errCh:
				require.Equal(t, types.NodeID("malformed-sender"), reported.NodeID,
					"the peer was reported for a message this node chose to shed")
			case <-time.After(30 * time.Second):
				t.Fatal("the reactor never reported the malformed message, so the test proves nothing")
			}
			require.Empty(t, errCh, "only the malformed message may be reported")
			require.Empty(t, cs.peerErrorQueue.ch, "shedding must not queue a peer error either")
			require.Equal(t, queued, cs.msgInfoQueue.lanes.buffered(),
				"the message must have been shed rather than queued")
			// Nothing consumes the queue here, so a message that never entered
			// it has by construction reached neither the write-ahead log nor
			// verification: both happen when the consensus goroutine takes it.
			require.Zero(t, wal.count(), "nothing may be written before a message is taken from the queue")
		})
	}
}

// The scheduler hands the consensus goroutine one message at a time and waits to
// be told that message is finished before looking at the verification budget
// again. That report must be made for every message handed over, including one
// the dispatcher finds no handler for and returns from immediately: a report
// that never comes leaves the scheduler waiting forever, and no peer is ever
// served again.
func TestSchedulerKeepsServingAfterAMessageNoHandlerTakes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cs, vss := makeState(ctx, t, makeStateArgs{validators: 4})
	stateData := cs.GetStateData()

	runCtx := dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	go cs.receiveRoutine(runCtx, nil)

	// A peer message the dispatcher has nothing to do with, so it never reaches
	// a handler or any middleware.
	require.NoError(t, cs.msgInfoQueue.send(ctx, nil, "peer"))

	// The peer's next message must still be served.
	vote := honestVote(ctx, t, vss[1], &stateData, 4)
	require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: vote}, "peer"))
	require.Eventually(t, func() bool { return voteAccepted(cs, vote) },
		30*time.Second, time.Millisecond,
		"the scheduler stopped serving peers after a message the dispatcher dropped")
}

// The consensus goroutine waits for the queue reader to finish before it returns,
// so a scheduler parked on the verification budget would keep the node from
// stopping at all — the process would have to be killed.
func TestShutdownDuringASchedulerWaitDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	clock := clockwork.NewFakeClock()
	inner := newVerificationBudget(verificationRate, withVerificationBudgetClock(clock))
	budget := &recordingVerificationBudget{inner: inner}
	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		stateOpts:  []StateOption{WithVerificationBudget(budget)},
	})
	stateData := cs.GetStateData()

	runCtx, stop := context.WithCancel(dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash))
	defer stop()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		cs.receiveRoutine(runCtx, nil)
	}()

	// Nothing ever advances the clock, so the message stays unaffordable and the
	// scheduler stays in its wait.
	drainVerificationBudget(inner)
	require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: honestVote(ctx, t, vss[1], &stateData, 4)}, "peer"))
	require.NoError(t, clock.BlockUntilContext(ctx, 1), "the peer message must be waiting for budget")

	stop()
	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		t.Fatal("the node did not stop while the scheduler waited for verification budget")
	}
}

// A peer going down takes its lane with it, and coming back gives it a fresh
// one. Left behind, the lane would keep taking turns from the peers that are
// still there, on behalf of messages nothing can do anything with.
func TestPeerDownRetiresTheLane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const peerID = types.NodeID("departing")
	cs, _ := makeState(ctx, t, makeStateArgs{validators: 4})
	r, _, _ := newReceivingReactor(ctx, t, cs, peerID)

	for i := 0; i < 10; i++ {
		require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: testPrecommitVote(0)}, peerID))
	}
	require.Equal(t, 10, cs.msgInfoQueue.lanes.buffered())

	r.peerDown(ctx, p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusDown}, channelBundle{})
	require.Zero(t, cs.msgInfoQueue.lanes.buffered(), "a disconnected peer's messages must be dropped")

	// Reconnecting is a clean slate rather than a resumption.
	require.NoError(t, cs.msgInfoQueue.send(ctx, &VoteMessage{Vote: testPrecommitVote(0)}, peerID))
	require.Equal(t, 1, cs.msgInfoQueue.lanes.buffered())
}

// A generation-g1 envelope that was still queued when g1 disconnected and the
// same NodeID reconnected as g2 must be dropped where the receive loop first
// sees it — before any rate limiter is charged, any proto is parsed, or any peer
// state is mutated — and never reported as a peer error. Enforcing the connection
// generation only at lane admission is too late: a malformed stale envelope fails
// MsgFromProto first, and that error is raised against the reconnected peer,
// while a well-formed one has already spent the peer's rate budget by then.
func TestStaleConnectionEnvelopeIsDroppedBeforePunishment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const peerID = types.NodeID("reconnector")
	cs, _ := makeState(ctx, t, makeStateArgs{validators: 4})

	logger := log.NewNopLogger()
	inCh := make(chan p2p.Envelope, 8)
	outCh := make(chan p2p.Envelope, 8)
	errCh := make(chan p2p.PeerError, 8)
	// A vote limiter holding a single token, metered against a clock that never
	// advances so a charge cannot be refilled away: if the stale envelope were
	// rate-limited, the token would be gone for good and observably so.
	rlClock := clockwork.NewFakeClock()
	r := &Reactor{
		logger:  logger,
		state:   cs,
		Metrics: NopMetrics(),
		peers: map[types.NodeID]*PeerState{
			peerID:             NewPeerState(logger, peerID),
			"malformed-sender": NewPeerState(logger, "malformed-sender"),
		},
		readySignal: make(chan struct{}),
		voteRateLimit: client.NewRateLimitWithBurst(ctx, 1, baseMessageCost, true, logger,
			client.WithRateLimitClock(rlClock)),
		dataRateLimit: client.NewRateLimitWithBurst(ctx, 0, 1, true, logger),
	}
	close(r.readySignal)
	voteCh := p2p.NewChannel(p2p.ConsensusVoteChannel, "vote", inCh, outCh, errCh)
	go r.processMsgCh(ctx, voteCh, channelBundle{vote: voteCh})

	// The peer is live on its second connection (generation g2); the envelope
	// below was left in flight by its first (generation g1).
	const g1, g2 = uint64(1), uint64(2)
	ps, ok := r.GetPeerState(peerID)
	require.True(t, ok)
	ps.SetLaneAdmission(g2, cs.msgInfoQueue.admitPeer(peerID))

	// A malformed envelope stamped with the ended generation. Reaching a handler
	// it would fail MsgFromProto and be reported against the reconnected peer;
	// charged first at the limiter it would spend the peer's only token.
	inCh <- p2p.Envelope{
		From:      peerID,
		ConnID:    g1,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{},
	}
	// A control message from another peer the reactor really must report. Messages
	// are handled in order, so its report arriving proves the stale one ahead of
	// it was already processed — without it, "no error yet" would prove nothing.
	inCh <- p2p.Envelope{
		From:      "malformed-sender",
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{},
	}

	select {
	case reported := <-errCh:
		require.Equal(t, types.NodeID("malformed-sender"), reported.NodeID,
			"the reconnected peer was reported for a message its ended connection left in flight")
	case <-time.After(30 * time.Second):
		t.Fatal("the reactor never reported the control message, so the test proves nothing")
	}
	require.Empty(t, errCh, "only the control message may be reported")
	require.Zero(t, cs.msgInfoQueue.lanes.buffered(), "a stale envelope must not reach the scheduler")

	// The peer's single rate-limit token is still there: the stale envelope was
	// dropped before the limiter, so it spent nothing, and a live vote-channel
	// message from the same peer is still admitted.
	require.True(t, r.allowVoteChannelMessage(ctx,
		&p2p.Envelope{From: peerID, ChannelID: p2p.ConsensusVoteChannel,
			Message: testVoteMsg(tmproto.PrevoteType, testBlockID(), 0)}),
		"the stale envelope consumed the reconnected peer's rate budget")
}

// newReceivingReactor builds a reactor over cs that receives on a real p2p
// channel, so a peer error raised by the message path is observable exactly as
// the router would see it.
func newReceivingReactor(
	ctx context.Context,
	t *testing.T,
	cs *State,
	peerID types.NodeID,
) (*Reactor, chan p2p.Envelope, chan p2p.PeerError) {
	t.Helper()
	logger := log.NewNopLogger()
	inCh := make(chan p2p.Envelope, 8)
	outCh := make(chan p2p.Envelope, 8)
	errCh := make(chan p2p.PeerError, 8)
	r := &Reactor{
		logger:  logger,
		state:   cs,
		Metrics: NopMetrics(),
		peers: map[types.NodeID]*PeerState{
			peerID:             NewPeerState(logger, peerID),
			"malformed-sender": NewPeerState(logger, "malformed-sender"),
		},
		readySignal: make(chan struct{}),
		// A small allowance, so the rate-limit case runs out quickly; the lane
		// overflow case never reaches it.
		voteRateLimit: client.NewRateLimitWithBurst(ctx, 1, voteRateBurst, true, logger),
		dataRateLimit: client.NewRateLimitWithBurst(ctx, 0, 1, true, logger),
	}
	close(r.readySignal)

	voteCh := p2p.NewChannel(p2p.ConsensusVoteChannel, "vote", inCh, outCh, errCh)
	go r.processMsgCh(ctx, voteCh, channelBundle{vote: voteCh})
	return r, inCh, errCh
}

// An envelope stamped with a connection generation whose NodeID has no peer
// state at all — the peer fully disconnected before its last in-flight message
// was dequeued — must be dropped where the receive loop first sees it, before
// that NodeID is charged at any rate limiter or its malformed payload is
// reported. Treating an absent peer state as live let such an envelope reach the
// limiter and the handler, spending the budget of and raising a peer error
// against a NodeID the node holds no connection for. An envelope with no
// generation (connID 0) keeps reaching the handlers, where an absent peer is
// no-op'd as before.
func TestStaleEnvelopeFromGonePeerIsDroppedBeforePunishment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const gonePeer = types.NodeID("gone")
	cs, _ := makeState(ctx, t, makeStateArgs{validators: 4})

	logger := log.NewNopLogger()
	inCh := make(chan p2p.Envelope, 8)
	outCh := make(chan p2p.Envelope, 8)
	errCh := make(chan p2p.PeerError, 8)
	// A vote limiter holding a single token, metered against a clock that never
	// advances so a spent token stays spent and observably so.
	rlClock := clockwork.NewFakeClock()
	r := &Reactor{
		logger:  logger,
		state:   cs,
		Metrics: NopMetrics(),
		// The gone peer is deliberately absent from the map: its connection ended
		// and its peer state was removed, which is exactly the state in which its
		// last in-flight message is dequeued.
		peers: map[types.NodeID]*PeerState{
			"control-sender": NewPeerState(logger, "control-sender"),
		},
		readySignal: make(chan struct{}),
		voteRateLimit: client.NewRateLimitWithBurst(ctx, 1, baseMessageCost, true, logger,
			client.WithRateLimitClock(rlClock)),
		dataRateLimit: client.NewRateLimitWithBurst(ctx, 0, 1, true, logger),
	}
	close(r.readySignal)
	voteCh := p2p.NewChannel(p2p.ConsensusVoteChannel, "vote", inCh, outCh, errCh)
	go r.processMsgCh(ctx, voteCh, channelBundle{vote: voteCh})

	// A malformed envelope carrying a nonzero connection generation from the gone
	// peer. Reaching a handler it would fail MsgFromProto and be reported against
	// that NodeID; charged first at the limiter it would spend its only token.
	inCh <- p2p.Envelope{
		From:      gonePeer,
		ConnID:    1,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{},
	}
	// A control message from a present peer the reactor really must report, so its
	// arrival proves the stale one ahead of it was already processed.
	inCh <- p2p.Envelope{
		From:      "control-sender",
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{},
	}

	select {
	case reported := <-errCh:
		require.Equal(t, types.NodeID("control-sender"), reported.NodeID,
			"a message from a peer that has fully disconnected was reported as a peer error")
	case <-time.After(30 * time.Second):
		t.Fatal("the reactor never reported the control message, so the test proves nothing")
	}
	require.Empty(t, errCh, "only the control message may be reported")

	// The gone peer's rate budget was never charged: the stale envelope was dropped
	// before the limiter, so a full-cost message under that NodeID is still admitted.
	require.True(t, r.allowVoteChannelMessage(ctx,
		&p2p.Envelope{From: gonePeer, ChannelID: p2p.ConsensusVoteChannel,
			Message: testVoteMsg(tmproto.PrevoteType, testBlockID(), 0)}),
		"a stale envelope from a gone peer consumed that NodeID's rate budget")
}

// flipOnErrorLogger swaps a peer's connection generation the first time the
// reactor logs a handler error, reproducing a reconnect that lands in the window
// between the top-of-loop liveness check and the peer-error raise. The swap runs
// synchronously inside the log call the error branch makes, so by the time the
// reactor re-checks liveness the envelope's connection is provably no longer the
// peer's live one — without any wall-clock race for the test to lose.
type flipOnErrorLogger struct {
	log.Logger
	once sync.Once
	flip func()
}

func (l *flipOnErrorLogger) fireOn(msg string) {
	if msg == "rejected peer message" || msg == "failed to process message" {
		l.once.Do(l.flip)
	}
}

func (l *flipOnErrorLogger) Debug(msg string, keyVals ...interface{}) {
	l.fireOn(msg)
	l.Logger.Debug(msg, keyVals...)
}

func (l *flipOnErrorLogger) Error(msg string, keyVals ...interface{}) {
	l.fireOn(msg)
	l.Logger.Error(msg, keyVals...)
}

// A handler error must not be reported as a peer error once the connection that
// produced the message has ended. The top-of-loop liveness check and the handler
// share no lock, so a reconnect (peerDown then peerUp swapping the NodeID's
// generation) can slip in between them; re-checking liveness before the peer
// error is raised is what keeps that window from blaming the reconnected peer for
// a message its live connection never sent. Without the re-check the error is
// raised against whichever generation the NodeID now holds.
func TestStaleConnectionHandlerErrorIsNotReportedAfterReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const peerID = types.NodeID("reconnector")
	const g1, g2 = uint64(1), uint64(2)
	cs, _ := makeState(ctx, t, makeStateArgs{validators: 4})

	base := log.NewNopLogger()
	inCh := make(chan p2p.Envelope, 8)
	outCh := make(chan p2p.Envelope, 8)
	errCh := make(chan p2p.PeerError, 8)

	ps := NewPeerState(base, peerID)
	hook := &flipOnErrorLogger{Logger: base}
	r := &Reactor{
		logger:  hook,
		state:   cs,
		Metrics: NopMetrics(),
		peers: map[types.NodeID]*PeerState{
			peerID:           ps,
			"control-sender": NewPeerState(base, "control-sender"),
		},
		readySignal:   make(chan struct{}),
		voteRateLimit: client.NewRateLimitWithBurst(ctx, 1, voteRateBurst, true, base),
		dataRateLimit: client.NewRateLimitWithBurst(ctx, 0, 1, true, base),
	}
	// The peer is live on generation g1 when the loop dequeues its message; the
	// hook reconnects it as g2 the instant the reactor logs the handler error —
	// after the top-of-loop check passed, before the peer error is raised.
	ps.SetLaneAdmission(g1, cs.msgInfoQueue.admitPeer(peerID))
	hook.flip = func() { ps.SetLaneAdmission(g2, cs.msgInfoQueue.admitPeer(peerID)) }

	close(r.readySignal)
	voteCh := p2p.NewChannel(p2p.ConsensusVoteChannel, "vote", inCh, outCh, errCh)
	go r.processMsgCh(ctx, voteCh, channelBundle{vote: voteCh})

	// A malformed envelope from the peer's live g1 connection: it passes the
	// top-of-loop check, fails MsgFromProto in the handler, and reaches the peer-
	// error raise — by which point the hook has reconnected the NodeID as g2.
	inCh <- p2p.Envelope{
		From:      peerID,
		ConnID:    g1,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{},
	}
	// A control message from a peer that never reconnects, whose report proves the
	// stale one ahead of it was already processed.
	inCh <- p2p.Envelope{
		From:      "control-sender",
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{},
	}

	select {
	case reported := <-errCh:
		require.Equal(t, types.NodeID("control-sender"), reported.NodeID,
			"a reconnected peer was reported for an error its ended connection produced")
	case <-time.After(30 * time.Second):
		t.Fatal("the reactor never reported the control message, so the test proves nothing")
	}
	require.Empty(t, errCh, "only the control message may be reported")
}

// assertWithinBudget checks that everything charged over the interval fits
// inside what the budget was configured to allow: its refill rate for as long as
// the run lasted, plus the bucket it started full with.
//
// It covers only the verification this cost model prices. Threshold recovery
// over an assembled vote set, and the round-trip to the application over a vote
// extension, are driven by the state machine rather than by one peer's message
// and are not charged here.
func assertWithinBudget(t *testing.T, charges []int, elapsed time.Duration) {
	t.Helper()
	total := 0
	for _, cost := range charges {
		total += cost
	}
	allowed := verificationRate*elapsed.Seconds() + verificationBudgetBurst
	require.LessOrEqual(t, float64(total), allowed,
		"more verification work was charged than the budget allows over %s", elapsed)
}

// assertFloodSettlesAtBaseCost checks that the attacker's commits really were
// charged the one verification the flood's arithmetic assumes.
//
// A message is charged for the work it actually does, not for what it declares,
// so an attacker that cannot forge a threshold signature is stopped at the first
// check and pays for that one. If such a message settled higher, the number of
// turns needed to hold the budget down would be different and the flood would
// not be the flood the caller claims to run.
func assertFloodSettlesAtBaseCost(t *testing.T, charges []int) {
	t.Helper()
	require.NotEmpty(t, charges, "nothing was charged, so no flood ran")
	for _, cost := range charges {
		require.Equal(t, baseMessageCost, cost,
			"a message the attacker cannot make good on was charged more than one verification")
	}
}

// forgedCommit is the cheapest message an attacker can use to hold the
// verification budget at zero: a commit for the height we are on, whose
// threshold signature cannot verify. Nothing deduplicates it, so every copy
// costs a turn and a signature check.
func forgedCommit(stateData *StateData) *types.Commit {
	return types.NewCommit(stateData.Height, stateData.Round, factory.MakeBlockID(), nil,
		&types.CommitSigns{
			QuorumSigns: types.QuorumSigns{BlockSign: make([]byte, types.SignatureSize)},
			QuorumHash:  stateData.Validators.QuorumHash,
		})
}

// honestVote signs a vote the state will verify and accept. A negative
// extension count asks for a prevote, the cheapest vote there is.
func honestVote(
	ctx context.Context,
	t *testing.T,
	vs *validatorStub,
	stateData *StateData,
	extensions int,
) *types.Vote {
	t.Helper()
	if extensions < 0 {
		vote, err := vs.signVote(ctx, tmproto.PrevoteType, stateData.state.ChainID, factory.MakeBlockID(),
			stateData.Validators.QuorumType, stateData.Validators.QuorumHash, nil)
		require.NoError(t, err)
		return vote
	}
	return signPrecommitWithExtensions(ctx, t, vs, stateData, extensions)
}

// voteAccepted reports whether the vote has completed verification and entered
// the vote set — the only observation that distinguishes a message that was
// verified from one that was merely dispatched.
func voteAccepted(cs *State, vote *types.Vote) bool {
	stateData := cs.GetStateData()
	if stateData.Votes == nil {
		return false
	}
	voteSet := stateData.Votes.GetVoteSet(vote.Round, vote.Type)
	return voteSet != nil && voteSet.GetByIndex(vote.ValidatorIndex) != nil
}

// advanceBudgetClock moves the fake clock forward only while something waits on
// it, and only as far as the next whole token of verification budget.
//
// Time therefore passes at exactly the rate the budget refills and stands still
// while the node works, so what a test measures between two readings of the
// clock is the delay the budget imposed and nothing else.
func advanceBudgetClock(ctx context.Context, clock *clockwork.FakeClock, budget *rateVerificationBudget) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if err := clock.BlockUntilContext(ctx, 1); err != nil {
				return
			}
			tokens := budget.limiter.TokensAt(clock.Now())
			toNextToken := (math.Floor(tokens) + 1 - tokens) / float64(budget.limiter.Limit())
			clock.Advance(time.Duration(toNextToken*float64(time.Second)) + time.Nanosecond)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// recordingWAL keeps the records written to it so a test can tell which messages
// the consensus goroutine got to.
type recordingWAL struct {
	nilWAL
	mtx  sync.Mutex
	msgs []WALMessage
}

func (w *recordingWAL) Write(msg WALMessage) error {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	w.msgs = append(w.msgs, msg)
	return nil
}

func (w *recordingWAL) WriteSync(msg WALMessage) error { return w.Write(msg) }

func (w *recordingWAL) count() int {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	return len(w.msgs)
}

func (w *recordingWAL) contains(match func(WALMessage) bool) bool {
	w.mtx.Lock()
	defer w.mtx.Unlock()
	for _, msg := range w.msgs {
		if match(msg) {
			return true
		}
	}
	return false
}

func isTimeoutRecord(msg WALMessage) bool {
	_, ok := msg.(timeoutInfo)
	return ok
}

func isLocalVoteRecord(vote *types.Vote) func(WALMessage) bool {
	return func(msg WALMessage) bool {
		mi, ok := msg.(msgInfo)
		if !ok || mi.PeerID != "" {
			return false
		}
		voteMsg, ok := mi.Msg.(*VoteMessage)
		return ok && voteMsg.Vote == vote
	}
}

func isPeerVoteRecord(vote *types.Vote) func(WALMessage) bool {
	return func(msg WALMessage) bool {
		mi, ok := msg.(msgInfo)
		if !ok || mi.PeerID == "" {
			return false
		}
		voteMsg, ok := mi.Msg.(*VoteMessage)
		return ok && voteMsg.Vote == vote
	}
}
