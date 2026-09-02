package consensus

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/cosmos/gogoproto/proto"
	"github.com/jonboulle/clockwork"
	"golang.org/x/time/rate"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	_ service.Service = (*Reactor)(nil)
	_ p2p.Wrapper     = (*tmcons.Message)(nil)
)

const (
	maxMsgSize = 1048576 // 1MB; NOTE: keep in sync with types.PartSet sizes.

	blocksToContributeToBecomeGoodPeer  = 10000
	votesToContributeToBecomeGoodPeer   = 10000
	commitsToContributeToBecomeGoodPeer = 10000

	// voteRateBurstCatchUp is the per-peer vote-channel front-load allowance, in
	// the verification work units the vote budget is charged in. It is
	// deliberately independent of the configured rate: a peer starts with a full
	// bucket, so this is what a single identity buys in one instant before the
	// sustained rate has any say, and deriving it from the rate would multiply
	// that front-load with every increase of the sustained allowance.
	//
	// An honest peer gossips on the order of ten vote-channel messages a second,
	// and the heaviest message Dash validators actually produce — a precommit
	// carrying four vote extensions — costs five work units. 200 therefore admits
	// about four seconds of an honest peer's heaviest gossip in a single instant,
	// comfortably covering a catch-up burst, while every default connection slot
	// — 64 connections plus the 4 the peer manager may hold on top while it
	// upgrades away lower-scored peers — front-loads 13600 units together: still
	// below the consensus queue they share, so even all-fresh identities cannot
	// fill it from their bursts alone.
	voteRateBurstCatchUp = 200

	// voteRateBurst is the size of the per-peer vote-channel token bucket.
	//
	// The floor matters: rate.Limiter rejects any request larger than the burst
	// permanently, no matter how long it waits. Since the budget is charged in
	// verification work, a bucket below the cost of a fully-extended precommit
	// would make that message permanently inadmissible — every validator's
	// precommits silently dropped and no block ever committed.
	voteRateBurst = max(voteRateBurstCatchUp, maxPeerMessageCost)

	// dataRateBurstMultiplier sets the per-peer data-channel rate-limit burst to
	// this multiple of the configured rate. The burst must stay comfortably above
	// proposalTokenCost, otherwise a proposal could never be admitted at all.
	dataRateBurstMultiplier = 2

	// proposalTokenCost is what one proposal charges against the data-channel
	// budget, relative to a block part's 1.
	//
	// A proposal forces a BLS signature verification and is not deduplicated: a
	// rejected proposal never becomes rs.Proposal, so every copy re-verifies.
	// A block part costs only part-set bookkeeping. Charging proposals more lets
	// the limit stay generous for block-part gossip — which must not be throttled,
	// since non-validators legitimately receive parts to catch up — while still
	// pricing proposals well above parts within one peer's allowance. What the
	// peers can force between them is bounded by the node-wide verification
	// budget, not here.
	proposalTokenCost = 5

	// The State and VoteSetBits channels verify no signature, so the node-wide
	// verification budget does not bound them at all. These are their own
	// ceilings, and like the others they are provisional until the load test.
	//
	// peerStateRateLimit is what one peer may spend per second across both
	// channels, and peerStateRateBurst what it may spend at once.
	//
	// Honest demand is dominated by HasVote, which a peer broadcasts for every
	// vote it adds: two per validator per round, so a 400-member quorum at
	// one-second rounds is 800/s from one peer. VoteSetMaj23 adds at most four
	// per PeerQueryMaj23SleepDuration, two a second at the default. This leaves
	// several times that while still bounding what one sender can occupy the
	// shared channel goroutine with.
	peerStateRateLimit = 5000
	peerStateRateBurst = 2 * peerStateRateLimit

	// maj23TokenCost is what one VoteSetMaj23 charges against that allowance,
	// relative to a state update's 1.
	//
	// It is the one message on these channels that asks this node for work: it
	// makes us build a bit array covering every validator — up to
	// types.MaxVotesCount bits — and send it back, which is far more than it
	// cost the sender to ask. The weight is what turns a limit sized for cheap
	// state updates into a tight one for the message that amplifies: fifty a
	// second per peer against the two an honest one sends.
	maj23TokenCost = 100

	// maj23ClaimsPerTick is how many majority claims a peer's gossip loop offers
	// in one pass: a prevote majority for its round, one for the proposal's POL
	// round, a precommit majority, and a catch-up commit.
	maj23ClaimsPerTick = 4

	// maj23NodeRateLimit and maj23NodeBurst are what this node answers
	// altogether, in messages a second and messages at once.
	//
	// Answering costs a bit array over every validator and a send, on the
	// goroutine that serves every peer's state messages, so what the node can
	// afford is a property of the node rather than of how often anyone gossips —
	// which is why no gossip cadence feeds into it. What does scale it is the
	// connection ceiling, since the shares below are handed out per connected
	// sender; an operator who raises that is buying a bigger node.
	//
	// Only this message type is bounded node-wide, and that is the point. A
	// ceiling over the cheap state updates too would be one several identities
	// could fill from inside their own legal allowances, and every honest peer's
	// round-step and vote announcements would then be dropped indiscriminately —
	// those are sent once, on a state change, and never repeated, so dropping
	// them leaves our picture of a peer stale and the gossip we send it wrong.
	// They cost a bit-set each and are already bounded per peer; the answer that
	// costs a bit array over every validator is what needs bounding across peers.
	maj23NodeRateLimit = 400
	maj23NodeBurst     = 2 * maj23NodeRateLimit

	// maj23AssumedSlots is the connection ceiling the node's answering capacity
	// is divided between: MaxConnected plus MaxConnectedUpgrade at their
	// defaults. A sender has to hold a slot to ask, so this is how many shares
	// there can be at once.
	//
	// It is an assumption rather than a reading — the ceiling is settled in the
	// p2p layer, which this one cannot see — so a test pins it against the
	// defaults it is taken from.
	maj23AssumedSlots = 68

	// maj23ReservedRateLimit is the half of the node's capacity handed out as
	// per-peer shares; maj23SurplusRateLimit is the half left to be contended
	// for by senders asking beyond their share.
	//
	// Splitting it is what keeps a ceiling from being first come, first served.
	// The slots an attacker holds can fill an unreserved ceiling from inside
	// their own private allowances, leaving the validators this node has to
	// reconcile votes with answered nothing — and that is the message a vote lost
	// to any of the other ceilings comes back through. Reserving half is the same
	// answer the per-peer scheduling lanes give one channel further in.
	maj23ReservedRateLimit = maj23NodeRateLimit / 2
	maj23SurplusRateLimit  = maj23NodeRateLimit - maj23ReservedRateLimit
	maj23SurplusBurst      = maj23NodeBurst - maj23AssumedSlots*maj23PeerShareBurst

	// maj23PeerShareRate is the majority claims a second one peer is answered
	// without drawing on the contended surplus: its equal share of the reserved
	// half.
	//
	// Honest demand is maj23ClaimsPerTick per PeerQueryMaj23SleepDuration — two a
	// second at the default cadence — so the share covers what a peer running
	// this protocol asks for, with margin. A cadence tuned faster than that asks
	// for more than the node has to give, and the shares are what make the
	// shortfall fall evenly rather than on whoever asks last.
	maj23PeerShareRate = float64(maj23ReservedRateLimit) / maj23AssumedSlots

	// maj23PeerShareBurst is how many claims one peer may have answered at once
	// out of its share: two whole gossip passes, so a peer catching up on a tick
	// it missed is answered in full.
	maj23PeerShareBurst = 2 * maj23ClaimsPerTick

	// voteSetResponseTimeout bounds how long the state channel's goroutine may
	// spend handing one VoteSetBits response to the router.
	//
	// That goroutine serves every peer's state messages in turn, so a send that
	// waits on a router which cannot take it right now stalls all of them — and
	// a peer that stops draining its own socket can make the router refuse to
	// take anything, so the wait is reachable on demand. Kept short enough that
	// even a peer spending its whole allowance on undeliverable answers costs a
	// fraction of the goroutine. A response this node cannot deliver promptly is
	// dropped; the peer asks again on its next gossip tick.
	voteSetResponseTimeout = 5 * time.Millisecond
)

var errReactorClosed = errors.New("reactor is closed")

// NOTE: Temporary interface for switching to block sync, we should get rid of v0.
// See: https://github.com/tendermint/tendermint/issues/4595
type BlockSyncReactor interface {
	SwitchToBlockSync(context.Context, sm.State) error

	GetMaxPeerBlockHeight() int64

	// GetTotalSyncedTime returns the time duration since the blocksync starting.
	GetTotalSyncedTime() time.Duration

	// GetRemainingSyncTime returns the estimating time the node will be fully synced,
	// if will return 0 if the blocksync does not perform or the number of block synced is
	// too small (less than 100).
	GetRemainingSyncTime() time.Duration
}

// Reactor defines a reactor for the consensus service.
type Reactor struct {
	service.BaseService
	logger log.Logger

	state    *State
	eventBus *eventbus.EventBus
	Metrics  *Metrics

	mtx         sync.RWMutex
	peers       map[types.NodeID]*PeerState
	waitSync    bool
	readySignal chan struct{} // closed when the node is ready to start consensus

	peerEvents p2p.PeerEventSubscriber
	chCreator  p2p.ChannelCreator

	// voteRateLimit drops vote-channel messages from a peer that exceeds its
	// per-peer rate, before signature verification. It bounds the CPU an
	// unprivileged peer can force. A no-op when the configured limit is 0.
	voteRateLimit *client.RateLimit

	// dataRateLimit does the same for the data channel, charging tokens by
	// verification cost so that what ONE peer can spend on proposals is bounded
	// without throttling the block-part gossip that peers rely on to catch up.
	// It says nothing about the aggregate across peers; that is the node-wide
	// verification budget's job.
	dataRateLimit *client.RateLimit

	// stateRateLimit bounds what one peer may spend on the State and VoteSetBits
	// channels, which the verification budget does not cover because they verify
	// no signatures.
	stateRateLimit *client.RateLimit

	// maj23PeerShare is the majority claims this node answers for a peer without
	// consulting the contended surplus. It is what keeps the node's answering
	// capacity from going to whoever asks first, which would leave the peers this
	// node most needs to reconcile votes with answered nothing.
	maj23PeerShare *client.RateLimit

	// maj23SurplusLimit is the ceiling on the majority claims this node answers
	// beyond the senders' own shares. The per-peer limiter cannot provide one:
	// identities are free, so what peers can ask for between them is bounded only
	// by how many an attacker brings.
	maj23SurplusLimit *rate.Limiter

	// clock is the time source every ceiling on this reactor is metered against,
	// and the one each peer's state dates its answers by; the wall clock unless a
	// test overrides it.
	clock clockwork.Clock

	// Context for controlling reactor goroutines
	ctx    context.Context
	cancel context.CancelFunc
}

// NewReactor returns a reference to a new consensus reactor, which implements
// the service.Service interface. It accepts a logger, consensus state, references
// to relevant p2p Channels and a channel to listen for peer updates on. The
// reactor will close all p2p Channels when stopping.
func NewReactor(
	logger log.Logger,
	cs *State,
	channelCreator p2p.ChannelCreator,
	peerEvents p2p.PeerEventSubscriber,
	eventBus *eventbus.EventBus,
	waitSync bool,
	metrics *Metrics,
) *Reactor {
	r := &Reactor{
		logger:      logger,
		state:       cs,
		waitSync:    waitSync,
		peers:       make(map[types.NodeID]*PeerState),
		eventBus:    eventBus,
		Metrics:     metrics,
		peerEvents:  peerEvents,
		chCreator:   channelCreator,
		readySignal: make(chan struct{}),
		clock:       clockwork.NewRealClock(),
	}
	r.BaseService = *service.NewBaseService(logger, "Consensus", r)

	if !waitSync {
		close(r.readySignal)
	}

	return r
}

type channelBundle struct {
	state   p2p.Channel
	data    p2p.Channel
	vote    p2p.Channel
	voteSet p2p.Channel
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed.
func (r *Reactor) OnStart(ctx context.Context) error {
	r.logger.Trace("consensus wait sync", "wait_sync", r.WaitSync())

	// Create reactor-specific context that will be canceled in OnStop
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Per-peer rate limiter for the vote channel. drop=true: over-limit
	// messages are dropped rather than delayed, so a flood from one peer cannot
	// block the shared channel goroutine (head-of-line) and honest peers on
	// other limiters are unaffected. A no-op when the configured limit is 0.
	//
	// The budget is denominated in verification work (see peerMessageCost), so a
	// fully-extended precommit costs maxPrecommitCost times a prevote.
	//
	// Burst is a fixed work allowance (see voteRateBurst) rather than a multiple
	// of the rate: vote-channel messages are individually expensive, and a peer
	// starts with a full bucket, so a burst that grew with the configured rate
	// would let one identity front-load seconds of verification in an instant.
	r.voteRateLimit = client.NewRateLimitWithBurst(r.ctx, r.state.config.PeerVoteRateLimit,
		voteRateBurst, true, r.logger)

	// Per-peer rate limiter for the data channel, same drop semantics. The
	// budget is denominated in verification cost (see proposalTokenCost), so it
	// can be set generously enough for block-part gossip — an honest peer emits
	// one data message per gossip tick, but that tick is operator-tunable and
	// catch-up runs it continuously — without leaving proposals unbounded.
	r.dataRateLimit = client.NewRateLimitWithBurst(r.ctx, r.state.config.PeerDataRateLimit,
		dataRateBurstFor(r.state.config.PeerDataRateLimit), true, r.logger)

	// The State and VoteSetBits channels are not covered by the verification
	// budget, so they carry ceilings of their own: one per peer, and one across
	// all of them, since a per-peer limit alone is worth only as much as node
	// identities are scarce.
	r.stateRateLimit = client.NewRateLimitWithBurst(r.ctx, peerStateRateLimit, peerStateRateBurst,
		true, r.logger, client.WithRateLimitClock(r.clock))
	r.maj23PeerShare = newMaj23PeerShareLimiter(r.ctx, r.logger, client.WithRateLimitClock(r.clock))
	r.maj23SurplusLimit = newMaj23SurplusLimiter()

	peerUpdates := r.peerEvents(r.ctx, "consensus")

	var chBundle channelBundle
	var err error

	chans := p2p.ConsensusChannelDescriptors()
	chBundle.state, err = r.chCreator(r.ctx, chans[p2p.ConsensusStateChannel])
	if err != nil {
		return err
	}

	chBundle.data, err = r.chCreator(r.ctx, chans[p2p.ConsensusDataChannel])
	if err != nil {
		return err
	}

	chBundle.vote, err = r.chCreator(r.ctx, chans[p2p.ConsensusVoteChannel])
	if err != nil {
		return err
	}

	chBundle.voteSet, err = r.chCreator(r.ctx, chans[p2p.VoteSetBitsChannel])
	if err != nil {
		return err
	}

	// start routine that computes peer statistics for evaluating peer quality
	//
	// TODO: Evaluate if we need this to be synchronized via WaitGroup as to not
	// leak the goroutine when stopping the reactor.
	go r.peerStatsRoutine(r.ctx, peerUpdates)

	// start routine that handles peer errors from the state machine
	go r.peerErrorRoutine(r.ctx, chBundle.state)

	r.subscribeToBroadcastEvents(r.ctx, chBundle.state)

	if !r.WaitSync() {
		if err := r.state.Start(r.ctx); err != nil {
			return err
		}
	} else if err := r.state.updateStateFromStore(); err != nil {
		return err
	}

	// Only state channel should be read during state sync.
	// Data, vote and vote set must wait.
	// We cannot skip waiting messages, as the peers might already have marked them as delivered.
	// XXX: this can lead to a deadlock, if so - we need additional buffer for (at least) Commits.
	go r.processMsgCh(r.ctx, chBundle.state, chBundle)
	go func() {
		select {
		case <-r.readySignal:
			go r.processMsgCh(r.ctx, chBundle.data, chBundle)
			go r.processMsgCh(r.ctx, chBundle.vote, chBundle)
			go r.processMsgCh(r.ctx, chBundle.voteSet, chBundle)
		case <-r.ctx.Done():
		}
	}()

	go r.processPeerUpdates(r.ctx, peerUpdates, chBundle)

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit, as well as unsubscribing from events and stopping
// state.
func (r *Reactor) OnStop() {
	// Cancel the reactor context to signal all goroutines to stop
	if r.cancel != nil {
		r.cancel()
	}

	r.state.Stop()

	if !r.WaitSync() {
		r.state.Wait()
	}
}

// WaitSync returns whether the consensus reactor is waiting for state/block sync.
func (r *Reactor) WaitSync() bool {
	select {
	case <-r.readySignal:
		// channel closed
		return false
	default:
		// channel is still open, so we still wait
		return true
	}
}

// SwitchToConsensus switches from block-sync mode to consensus mode. It resets
// the state, turns off block-sync, and starts the consensus state-machine.
//
// skipWAL says the node needs no WAL catchup. behind says block sync stopped
// while a peer still claimed a height above ours, and holds back proposals until
// the node has caught up - see catchupTracker.
func (r *Reactor) SwitchToConsensus(ctx context.Context, state sm.State, skipWAL bool, behind bool) {
	r.logger.Info("switching to consensus", "behind", behind)

	if behind {
		r.state.catchup.arm(r.maxPeerHeight)
	}

	stateData := r.state.GetStateData()
	// we have no votes, so reconstruct LastCommit from SeenCommit
	if state.LastBlockHeight > 0 {
		var err error
		stateData.LastCommit, err = r.state.loadLastCommit(state.LastBlockHeight)
		if err != nil {
			panic(err)
		}
	}

	// NOTE: The line below causes broadcastNewRoundStepRoutine() to broadcast a
	// NewRoundStepMessage.
	stateData.updateToState(state, nil, r.state.blockStore)
	err := r.state.stateDataStore.Update(stateData)
	if err != nil {
		panic(err)
	}
	r.state.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

	if err := r.state.Start(ctx); err != nil {
		panic(fmt.Sprintf(`failed to start consensus state: %v

conS:
%+v

conR:
%+v`, err, r.state, r))
	}

	close(r.readySignal)

	r.Metrics.BlockSyncing.Set(0)
	r.Metrics.StateSyncing.Set(0)

	if skipWAL {
		r.state.doWALCatchup = false
	}

	d := types.EventDataBlockSyncStatus{Complete: true, Height: state.LastBlockHeight}
	if err := r.eventBus.PublishEventBlockSyncStatus(d); err != nil {
		r.logger.Error("failed to emit the blocksync complete event", "err", err)
	}
}

// maxPeerHeight returns the highest height any connected peer reports, and 0
// when none has reported one yet.
func (r *Reactor) maxPeerHeight() int64 {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	var height int64
	for _, ps := range r.peers {
		height = max(height, ps.GetHeight())
	}
	return height
}

// String returns a string representation of the Reactor.
//
// NOTE: For now, it is just a hard-coded string to avoid accessing unprotected
// shared variables.
//
// TODO: improve!
func (r *Reactor) String() string {
	return "ConsensusReactor"
}

// GetPeerState returns PeerState for a given NodeID.
func (r *Reactor) GetPeerState(peerID types.NodeID) (*PeerState, bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	ps, ok := r.peers[peerID]
	return ps, ok
}

// subscribeToBroadcastEvents subscribes for new round steps and votes using the
// internal pubsub defined in the consensus state to broadcast them to peers
// upon receiving.
func (r *Reactor) subscribeToBroadcastEvents(ctx context.Context, stateCh p2p.Channel) {
	onStopCh := r.state.getOnStopCh()

	r.state.emitter.AddListener(
		types.EventNewRoundStepValue,
		func(data eventemitter.EventData) error {
			rs := data.(*cstypes.RoundState)
			err := r.broadcast(ctx, stateCh, rs.NewRoundStepMessage())
			if err != nil {
				return err
			}
			r.logResult(err, r.logger, "broadcasting round step message", "height", rs.Height, "round", rs.Round)
			select {
			case onStopCh <- data.(*cstypes.RoundState):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		},
	)

	r.state.emitter.AddListener(
		types.EventValidBlockValue,
		func(data eventemitter.EventData) error {
			rs := data.(*cstypes.RoundState)
			err := r.broadcast(ctx, stateCh, rs.NewValidBlockMessage())
			r.logResult(err, r.logger, "broadcasting new valid block message", "height", rs.Height, "round", rs.Round)
			return err
		},
	)

	r.state.emitter.AddListener(
		types.EventVoteValue,
		func(data eventemitter.EventData) error {
			vote := data.(*types.Vote)
			err := r.broadcast(ctx, stateCh, vote.HasVoteMessage())
			r.logResult(err, r.logger, "broadcasting HasVote message", "height", vote.Height, "round", vote.Round)
			return err
		},
	)

	r.state.emitter.AddListener(types.EventCommitValue,
		func(data eventemitter.EventData) error {
			commit := data.(*types.Commit)
			err := r.broadcast(ctx, stateCh, commit.HasCommitMessage())
			r.logResult(err, r.logger, "broadcasting HasVote message", "height", commit.Height, "round", commit.Round)
			return err
		},
	)
}

// broadcast sends a broadcast message to all peers connected to the `channel`.
func (r *Reactor) broadcast(ctx context.Context, channel p2p.Channel, msg proto.Message) error {
	select {
	case <-ctx.Done():
		return errReactorClosed
	default:
		return channel.Send(ctx, p2p.Envelope{
			Broadcast: true,
			Message:   msg,
		})
	}
}

// logResult creates a log that depends on value of err
func (r *Reactor) logResult(err error, logger log.Logger, message string, keyvals ...interface{}) bool {
	if err != nil {
		logger.Error(message+" error", append(keyvals, "error", err))
		return false
	}
	logger.Trace(message+" success", keyvals...)
	return true
}

// processPeerUpdate process a peer update message. For new or reconnected peers,
// we create a peer state if one does not exist for the peer, which should always
// be the case, and we spawn all the relevant goroutine to broadcast messages to
// the peer. During peer removal, we remove the peer for our set of peers and
// signal to all spawned goroutines to gracefully exit in a non-blocking manner.
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate, chans channelBundle) {
	r.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status,
		"peer_proTxHash", peerUpdate.ProTxHash.ShortString())

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		// Do not allow starting new broadcasting goroutines after reactor shutdown
		// has been initiated. This can happen after we've manually closed all
		// peer goroutines, but the router still sends in-flight peer updates.
		if !r.IsRunning() {
			return
		}
		r.peerUp(ctx, peerUpdate, 3, chans)
	case p2p.PeerStatusDown:
		r.peerDown(ctx, peerUpdate, chans)
	}
}

// peerUp starts the peer. It recursively retries up to `retries` times if the peer is already closing.
func (r *Reactor) peerUp(ctx context.Context, peerUpdate p2p.PeerUpdate, retries int, chans channelBundle) {
	if retries < 1 {
		r.logger.Error("peer up failed: max retries exceeded", "peer", peerUpdate.NodeID)
		return
	}

	r.mtx.Lock()
	defer r.mtx.Unlock()

	ps, ok := r.peers[peerUpdate.NodeID]
	if !ok {
		ps = NewPeerState(r.logger, peerUpdate.NodeID,
			WithPeerStateClock(r.clock),
			WithMaj23AnswerTTL(maj23AnswerTTLFor(r.state.config.PeerQueryMaj23SleepDuration)))
		ps.SetProTxHash(peerUpdate.ProTxHash)
		r.peers[peerUpdate.NodeID] = ps
	} else if len(peerUpdate.ProTxHash) > 0 {
		ps.SetProTxHash(peerUpdate.ProTxHash)
	}
	// Admit this connection to the scheduler and record the generation and session
	// it stands for on the peer state, so the peer's messages can be told apart
	// from any an earlier, now-purged connection left in flight: a message carries
	// the immutable generation the router stamped on it, and is admitted only while
	// that generation is still the peer's live one. A connection that never went
	// down keeps its session, so a repeated up does not strand its messages.
	ps.SetLaneAdmission(peerUpdate.ConnID, r.state.msgInfoQueue.admitPeer(peerUpdate.NodeID))

	logger := r.logger.With(
		"peer", ps.peerID,
		"peer_proTxHash", ps.GetProTxHash().ShortString(),
	)
	// TODO needs to register this gossip worker, to be able to stop it once a peer will be down
	msgSender := p2pMsgSender{logger: logger, ps: ps, chans: chans}
	pgw := newPeerGossipWorker(logger, ps, r.state, &msgSender)

	select {
	case <-ctx.Done():
		// Hmm, someone is closing this peer right now, let's wait and retry
		// Note: we run this in a goroutine to not block main goroutine in ps.broadcastWG.Wait()
		go func() {
			time.Sleep(r.state.config.PeerGossipSleepDuration)
			r.peerUp(ctx, peerUpdate, retries-1, chans)
		}()
		return
	default:
	}

	if !ps.IsRunning() {
		// Set the peer state's closer to signal to all spawned goroutines to exit
		// when the peer is removed. We also set the running state to ensure we
		// do not spawn multiple instances of the same goroutines and finally we
		// set the waitgroup counter so we know when all goroutines have exited.
		ps.SetRunning(true)
		ctx, ps.cancel = context.WithCancel(ctx)

		go func() {
			select {
			case <-ctx.Done():
				return
			case <-r.readySignal:
			}
			// do nothing if the peer has
			// stopped while we've been waiting.
			if !ps.IsRunning() {
				return
			}
			// start goroutines for this peer
			_ = pgw.Start(ctx)

			// Send our state to the peer. If we're block-syncing, broadcast a
			// RoundStepMessage later upon SwitchToConsensus().
			if !r.WaitSync() {
				go func() {
					rs := r.state.GetRoundState()
					err := msgSender.send(ctx, rs.NewRoundStepMessage())
					r.logResult(err, r.logger, "sending round step msg", "height", rs.Height, "round", rs.Round)
				}()
			}
		}()
	}
}

func (r *Reactor) peerDown(_ context.Context, peerUpdate p2p.PeerUpdate, _chans channelBundle) {
	// Retire the peer's scheduling lane: what it left queued can no longer help
	// us make progress, and an abandoned lane would keep taking turns from the
	// peers still connected. Done outside the reactor's own lock, so the lane
	// lock stays a leaf and cannot take part in a lock cycle.
	r.state.msgInfoQueue.purgePeer(peerUpdate.NodeID)

	// Delete and cancel the peer state synchronously. Peer updates are processed
	// one at a time on a single goroutine, so a following up for a reconnecting
	// NodeID sees the state already gone and builds a fresh one — it cannot reuse
	// a state a pending down still owns, nor have that down later delete or cancel
	// the reconnection's state. cancel only signals the peer's goroutines to stop;
	// it does not wait on them, so doing this in line does not hold up the update
	// loop.
	r.mtx.Lock()
	ps, ok := r.peers[peerUpdate.NodeID]
	if ok {
		delete(r.peers, peerUpdate.NodeID)
	}
	r.mtx.Unlock()

	if ok && ps.IsRunning() {
		// signal to all spawned goroutines for the peer to gracefully exit
		ps.SetRunning(false)
		ps.cancel()
	}
}

// handleStateMessage handles envelopes sent from peers on the StateChannel.
// An error is returned if the message is unrecognized or if validation fails.
// If we fail to find the peer state for the envelope sender, we perform a no-op
// and return. This can happen when we process the envelope after the peer is
// removed.
func (r *Reactor) handleStateMessage(ctx context.Context, envelope *p2p.Envelope, msgI Message, voteSetCh p2p.Channel) error {
	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		r.logger.Debug("failed to find peer state", "peer", envelope.From, "ch_id", "StateChannel")
		return nil
	}

	switch msg := envelope.Message.(type) {
	case *tmcons.NewRoundStep:
		stateData := r.state.GetStateData()
		initialHeight := stateData.InitialHeight()

		if err := msgI.(*NewRoundStepMessage).ValidateHeight(initialHeight); err != nil {
			// A round-step announcement that cannot be true of this chain is free
			// to produce and the cheapest thing the state channel carries, so an
			// Error line per rejection is a log flood for the asking. The message
			// itself is never echoed, whatever the level.
			r.logger.Debug("peer sent us an invalid round step", "err", err)
			return err
		}

		ps.ApplyNewRoundStepMessage(msgI.(*NewRoundStepMessage))

	case *tmcons.NewValidBlock:
		ps.ApplyNewValidBlockMessage(msgI.(*NewValidBlockMessage))

	case *tmcons.HasCommit:
		ps.ApplyHasCommitMessage(msgI.(*HasCommitMessage))

	case *tmcons.HasVote:
		if err := ps.ApplyHasVoteMessage(msgI.(*HasVoteMessage)); err != nil {
			// A vote index outside the validator set we have for that height is
			// free for a peer to name, so logging one at Error would be a line
			// per message for anyone who cares to send them.
			r.logger.Debug("applying HasVote message failed", "err", err)
			return err
		}
	case *tmcons.VoteSetMaj23:
		stateData := r.state.GetStateData()
		height, votes := stateData.HeightVoteSet()

		if height != msg.Height {
			r.logger.Debug("vote set height does not match msg height", "height", height, "msg", msg)
			return nil
		}
		vsmMsg := msgI.(*VoteSetMaj23Message)

		// peer claims to have a maj23 for some BlockID at <H,R,S>
		err := votes.SetPeerMaj23(msg.Height, msg.Round, msg.Type, ps.peerID, vsmMsg.BlockID)
		if err != nil {
			return err
		}

		// Respond with a VoteSetBitsMessage showing which votes we have and
		// consequently shows which we don't have.
		var ourVotes *bits.BitArray
		switch vsmMsg.Type {
		case tmproto.PrevoteType:
			ourVotes = votes.Prevotes(msg.Round).BitArrayByBlockID(vsmMsg.BlockID)

		case tmproto.PrecommitType:
			ourVotes = votes.Precommits(msg.Round).BitArrayByBlockID(vsmMsg.BlockID)

		default:
			panic("bad VoteSetBitsMessage field type; forgot to add a check in ValidateBasic?")
		}

		// A peer's gossip loop repeats the same claim on every tick, and a repeat
		// just answered is passed over: it costs a bit array over every validator
		// and the answer has not changed. It is not passed over for long, because
		// an unchanged answer is also what tells the peer a vote it believes it
		// delivered never arrived.
		if !ps.ShouldAnswerVoteSetMaj23(msg.Height, msg.Round, msg.Type, vsmMsg.BlockID, ourVotes) {
			return nil
		}

		eMsg := &tmcons.VoteSetBits{
			Height:  msg.Height,
			Round:   msg.Round,
			Type:    msg.Type,
			BlockID: msg.BlockID,
		}

		if votesProto := ourVotes.ToProto(); votesProto != nil {
			eMsg.Votes = *votesProto
		}

		if r.sendVoteSetBits(ctx, voteSetCh, envelope.From, eMsg) {
			ps.RecordVoteSetMaj23Answer(msg.Height, msg.Round, msg.Type, vsmMsg.BlockID, ourVotes)
		}

	default:
		return fmt.Errorf("received unknown message on StateChannel: %T", msg)
	}

	return nil
}

// laneCtx binds the scheduler session for an envelope to its context, deriving
// it from the connection generation the router stamped on the envelope at
// ingress rather than from the peer state's current session. It reports false
// when the envelope was produced by a connection that is no longer this peer's
// live one — a message an ended connection left in flight, which a reconnect
// under the same NodeID would otherwise let inherit the new connection's session.
//
// Reading the generation from the envelope, not from the mutable peer state, is
// what closes that: the generation is captured before the message could be
// buffered across a reconnect, so it names the connection that actually produced
// the message however long it then waited. A stale message is a silent local
// drop, never a peer error — an ended connection is nobody's fault.
func (r *Reactor) laneCtx(ctx context.Context, ps *PeerState, envelope *p2p.Envelope) (context.Context, bool) {
	session, ok := ps.laneSessionForConn(envelope.ConnID)
	if !ok {
		return nil, false
	}
	return ctxWithPeerLaneSession(ctx, session), true
}

// envelopeFromLiveConn reports whether the envelope was produced by the peer's
// currently live connection, matching the immutable generation the router
// stamped on it at ingress against the generation recorded at peer-up. It is the
// same liveness test lane admission applies in laneCtx, hoisted to the top of the
// receive loop so a stale envelope is refused before it can be rate-limited,
// parsed, or mutate any peer state — the punitive and stateful steps that the
// admission check, running only after them, is too late to protect.
//
// A peer with no state at all is judged by the generation the envelope carries.
// One stamped with a connection generation (a nonzero connID) but whose NodeID
// has no live peer state is a message an ended connection left in flight after
// the peer disconnected — the peer is gone, so it is stale and refused here
// rather than reaching a rate limiter or handler for a NodeID that is no longer
// present. One carrying no generation (connID 0, the test and direct paths that
// predate ingress stamping) matches a peer that likewise has none, so those
// paths are unaffected and their messages reach the handlers and are no-op'd
// there exactly as before.
func (r *Reactor) envelopeFromLiveConn(envelope *p2p.Envelope) bool {
	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		return envelope.ConnID == 0
	}
	_, live := ps.laneSessionForConn(envelope.ConnID)
	return live
}

// handleDataMessage handles envelopes sent from peers on the DataChannel. If we
// fail to find the peer state for the envelope sender, we perform a no-op and
// return. This can happen when we process the envelope after the peer is
// removed.
func (r *Reactor) handleDataMessage(ctx context.Context, envelope *p2p.Envelope, msgI Message) error {
	logger := r.logger.With("peer", envelope.From, "ch_id", "DataChannel")

	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		r.logger.Debug("failed to find peer state")
		return nil
	}

	if r.WaitSync() {
		logger.Debug("ignoring message received during sync", "msg", tmstrings.LazySprintf("%T", msgI))
		return nil
	}

	logger.Trace("data channel processing", "msg", envelope.Message, "type", fmt.Sprintf("%T", envelope.Message))

	switch msg := envelope.Message.(type) {
	case *tmcons.Proposal:
		pMsg := msgI.(*ProposalMessage)

		ps.SetHasProposal(pMsg.Proposal)
		laneCtx, ok := r.laneCtx(ctx, ps, envelope)
		if !ok {
			return nil
		}
		return r.state.sendMessage(laneCtx, pMsg, envelope.From)
	case *tmcons.ProposalPOL:
		ps.ApplyProposalPOLMessage(msgI.(*ProposalPOLMessage))
	case *tmcons.BlockPart:
		bpMsg := msgI.(*BlockPartMessage)

		ps.SetHasProposalBlockPart(bpMsg.Height, bpMsg.Round, int(bpMsg.Part.Index))
		r.Metrics.BlockParts.With("peer_id", string(envelope.From)).Add(1)
		laneCtx, ok := r.laneCtx(ctx, ps, envelope)
		if !ok {
			return nil
		}
		return r.state.sendMessage(laneCtx, bpMsg, envelope.From)
	default:
		return fmt.Errorf("received unknown message on DataChannel: %T", msg)
	}

	return nil
}

// handleVoteMessage handles envelopes sent from peers on the VoteChannel. If we
// fail to find the peer state for the envelope sender, we perform a no-op and
// return. This can happen when we process the envelope after the peer is
// removed.
func (r *Reactor) handleVoteMessage(ctx context.Context, envelope *p2p.Envelope, msgI Message) error {
	logger := r.logger.With("peer", envelope.From, "ch_id", "VoteChannel")

	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		logger.Debug("failed to find peer state")
		return nil
	}

	if r.WaitSync() {
		logger.Debug("ignoring message received during sync", "msg", msgI)
		return nil
	}

	logger.Trace("vote channel processing", "msg", envelope.Message, "type", fmt.Sprintf("%T", envelope.Message))

	switch msg := envelope.Message.(type) {
	case *tmcons.Commit:
		c, err := types.CommitFromProto(msg.Commit)
		if err != nil {
			return err
		}
		ps.SetHasCommit(c)

		cMsg := msgI.(*CommitMessage)
		laneCtx, ok := r.laneCtx(ctx, ps, envelope)
		if !ok {
			return nil
		}
		err = r.state.sendMessage(laneCtx, cMsg, envelope.From)
		if err != nil {
			return err
		}
	case *tmcons.Vote:
		stateData := r.state.stateDataStore.Get()

		isValidator := stateData.isValidator(r.state.privValidator.ProTxHash)
		height, valSize := stateData.Height, stateData.Validators.Size()
		lastValSize := len(stateData.LastValidators.Validators)

		if isValidator { // ignore votes on non-validator nodes; TODO don't even send it
			vMsg := msgI.(*VoteMessage)

			if err := vMsg.Vote.ValidateBasic(); err != nil {
				return fmt.Errorf("invalid vote received from %s: %w", envelope.From, err)
			}

			ps.EnsureVoteBitArrays(height, valSize)
			ps.EnsureVoteBitArrays(height-1, lastValSize)
			if err := ps.SetHasVote(vMsg.Vote); err != nil {
				return err
			}
			laneCtx, ok := r.laneCtx(ctx, ps, envelope)
			if !ok {
				return nil
			}
			return r.state.sendMessage(laneCtx, vMsg, envelope.From)
		}
	default:
		return fmt.Errorf("received unknown message on VoteChannel: %T", msg)
	}

	return nil
}

// handleVoteSetBitsMessage handles envelopes sent from peers on the
// VoteSetBitsChannel. If we fail to find the peer state for the envelope sender,
// we perform a no-op and return. This can happen when we process the envelope
// after the peer is removed.
func (r *Reactor) handleVoteSetBitsMessage(_ context.Context, envelope *p2p.Envelope, msgI Message) error {
	logger := r.logger.With("peer", envelope.From, "ch_id", "VoteSetBitsChannel")

	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		r.logger.Debug("failed to find peer state")
		return nil
	}

	if r.WaitSync() {
		logger.Debug("ignoring message received during sync", "msg", msgI)
		return nil
	}

	switch msg := envelope.Message.(type) {
	case *tmcons.VoteSetBits:
		stateData := r.state.GetStateData()
		height, votes := stateData.Height, stateData.Votes

		vsbMsg := msgI.(*VoteSetBitsMessage)

		if height == msg.Height {
			var ourVotes *bits.BitArray

			switch msg.Type {
			case tmproto.PrevoteType:
				ourVotes = votes.Prevotes(msg.Round).BitArrayByBlockID(vsbMsg.BlockID)

			case tmproto.PrecommitType:
				ourVotes = votes.Precommits(msg.Round).BitArrayByBlockID(vsbMsg.BlockID)

			default:
				panic("bad VoteSetBitsMessage field type; forgot to add a check in ValidateBasic?")
			}

			ps.ApplyVoteSetBitsMessage(vsbMsg, ourVotes)
		} else {
			ps.ApplyVoteSetBitsMessage(vsbMsg, nil)
		}

	default:
		return fmt.Errorf("received unknown message on VoteSetBitsChannel: %T", msg)
	}

	return nil
}

// handleMessage handles an Envelope sent from a peer on a specific p2p Channel.
// It will handle errors and any possible panics gracefully. A caller can handle
// any error returned by sending a PeerError on the respective channel.
//
// NOTE: We process these messages even when we're block syncing. Messages affect
// either a peer state or the consensus state. Peer state updates can happen in
// parallel, but processing of proposals, block parts, and votes are ordered by
// the p2p channel.
//
// NOTE: We block on consensus state for proposals, block parts, and votes.
func (r *Reactor) handleMessage(ctx context.Context, envelope *p2p.Envelope, chans channelBundle) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic in processing message: %v", e)
			r.logger.Error(
				"recovering from processing message panic",
				"err", err,
				"stack", string(debug.Stack()),
			)
		}
	}()

	msg, err := MsgFromProto(envelope.Message)
	if err != nil {
		return err
	}

	switch envelope.ChannelID {
	case p2p.ConsensusStateChannel:
		err = r.handleStateMessage(ctx, envelope, msg, chans.voteSet)
	case p2p.ConsensusDataChannel:
		err = r.handleDataMessage(ctx, envelope, msg)
	case p2p.ConsensusVoteChannel:
		err = r.handleVoteMessage(ctx, envelope, msg)
	case p2p.VoteSetBitsChannel:
		err = r.handleVoteSetBitsMessage(ctx, envelope, msg)
	default:
		err = fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}

	return err
}

// processMsgCh initiates a blocking process where we listen for and handle
// envelopes on the StateChannel or DataChannel or VoteChannel or VoteSetBitsChannel.
// Any error encountered during message execution will result in a PeerError being sent
// on the StateChannel or DataChannel or VoteChannel or VoteSetBitsChannel.
// When the reactor is stopped, we will catch the signal and close the p2p Channel gracefully.
// allowVoteChannelMessage applies the per-peer vote-channel rate limit. It
// returns false for a message that should be dropped because the sending peer
// is over its budget. Messages on other channels, and all messages when the
// limit is disabled (0), are always allowed. On limiter error it fails open.
//
// The budget is charged in verification work rather than message count, so a
// peer cannot buy a fully-extended precommit — up to 66 signature verifications
// — for the price of a prevote. A message whose work cannot be priced is
// dropped locally, without a peer error: it is never punished for what it
// declares, only refused.
func (r *Reactor) allowVoteChannelMessage(ctx context.Context, envelope *p2p.Envelope) bool {
	if envelope.ChannelID != p2p.ConsensusVoteChannel {
		return true
	}
	cost, err := peerMessageCost(envelope.Message)
	if err != nil {
		r.logger.Debug("dropping unpriceable vote-channel message", "peer", envelope.From, "err", err)
		return false
	}
	allowed, err := r.voteRateLimit.Limit(ctx, envelope.From, cost)
	if err != nil {
		r.logger.Error("vote rate limiter failed", "peer", envelope.From, "err", err)
		return true
	}
	if !allowed {
		r.logger.Debug("dropping vote-channel message over per-peer rate limit",
			"peer", envelope.From, "cost", cost)
	}
	return allowed
}

// dataRateBurstFor returns the token bucket size for a data-channel limit.
//
// The floor matters: rate.Limiter.AllowN rejects any request larger than the
// burst permanently, no matter how long it waits. With a burst below
// proposalTokenCost a node would silently never admit a proposal and could not
// reach consensus, so an aggressively low configured limit must still leave room
// for the single most expensive message.
func dataRateBurstFor(limit float64) int {
	burst := int(dataRateBurstMultiplier * limit)
	if burst < proposalTokenCost {
		return proposalTokenCost
	}
	return burst
}

// dataChannelMessageCost returns the token cost of a data-channel message.
// Proposals are charged more because each one forces a BLS verification that no
// dedup avoids; block parts and POL bit arrays cost one.
func dataChannelMessageCost(msg proto.Message) int {
	if _, ok := msg.(*tmcons.Proposal); ok {
		return proposalTokenCost
	}
	return 1
}

// allowDataChannelMessage applies the per-peer data-channel rate limit. It
// returns false for a message that should be dropped because the sending peer is
// over its budget. Messages on other channels, and all messages when the limit
// is disabled (0), are always allowed. On limiter error it fails open.
func (r *Reactor) allowDataChannelMessage(ctx context.Context, envelope *p2p.Envelope) bool {
	if envelope.ChannelID != p2p.ConsensusDataChannel {
		return true
	}
	cost := dataChannelMessageCost(envelope.Message)
	allowed, err := r.dataRateLimit.Limit(ctx, envelope.From, cost)
	if err != nil {
		r.logger.Error("data rate limiter failed", "peer", envelope.From, "err", err)
		return true
	}
	if !allowed {
		r.logger.Debug("dropping data-channel message over per-peer rate limit",
			"peer", envelope.From, "cost", cost)
	}
	return allowed
}

// newMaj23SurplusLimiter returns the ceiling on the majority claims answered
// beyond the senders' own shares.
func newMaj23SurplusLimiter() *rate.Limiter {
	return rate.NewLimiter(rate.Limit(maj23SurplusRateLimit), maj23SurplusBurst)
}

// newMaj23PeerShareLimiter returns the per-peer share of the node's answering
// capacity.
func newMaj23PeerShareLimiter(
	ctx context.Context,
	logger log.Logger,
	opts ...client.RateLimitOptionFunc,
) *client.RateLimit {
	return client.NewRateLimitWithBurst(ctx, maj23PeerShareRate, maj23PeerShareBurst, true, logger, opts...)
}

// stateChannelMessageCost returns what a State- or VoteSetBits-channel message
// charges. A VoteSetMaj23 asks this node to build and send a bit array over
// every validator; everything else on these channels only updates a peer's
// recorded round state.
func stateChannelMessageCost(msg proto.Message) int {
	if _, ok := msg.(*tmcons.VoteSetMaj23); ok {
		return maj23TokenCost
	}
	return 1
}

// allowStateChannelMessage applies the ceilings for the State and VoteSetBits
// channels. It returns false for a message that should be dropped. Messages on
// other channels are always allowed.
//
// Over-ceiling is a local drop and never a peer offense. The peer that reaches
// its allowance under load is as likely to be an honest one gossiping hard as a
// hostile one, and neither can be told apart from here.
func (r *Reactor) allowStateChannelMessage(ctx context.Context, envelope *p2p.Envelope) bool {
	if envelope.ChannelID != p2p.ConsensusStateChannel && envelope.ChannelID != p2p.VoteSetBitsChannel {
		return true
	}
	cost := stateChannelMessageCost(envelope.Message)
	// A majority claim is only answered when it arrives where the answering code
	// reads it. Nothing binds a message type to a channel on the wire, so one
	// tagged for the VoteSetBits channel reaches a different goroutine and draws
	// no answer at all; it is still priced as the request it is, but it must not
	// spend the capacity set aside for claims this node will act on.
	if _, ok := envelope.Message.(*tmcons.VoteSetMaj23); ok && envelope.ChannelID == p2p.ConsensusStateChannel {
		return r.allowMaj23Claim(ctx, envelope, cost)
	}
	return r.chargeStateAllowance(ctx, envelope, cost)
}

// chargeStateAllowance charges a message against what its sender may spend on
// the State and VoteSetBits channels, and reports whether it may be handled.
// That allowance is what keeps one sender from occupying the goroutine those
// channels share.
func (r *Reactor) chargeStateAllowance(ctx context.Context, envelope *p2p.Envelope, cost int) bool {
	allowed, err := r.stateRateLimit.Limit(ctx, envelope.From, cost)
	if err != nil {
		r.logger.Error("state rate limiter failed", "peer", envelope.From, "err", err)
		return true
	}
	if !allowed {
		r.Metrics.StateChannelDrops.Add(1)
		r.logger.Debug("dropping state-channel message over per-peer rate limit",
			"peer", envelope.From, "ch_id", envelope.ChannelID, "cost", cost)
		return false
	}
	return true
}

// allowMaj23Claim decides one majority claim, the only message on these
// channels that asks this node to build and send an answer.
//
// Every peer is answered up to its own share whatever the others are doing, and
// only what a sender asks for beyond that share competes for the surplus. A
// ceiling with nothing reserved would be first come, first served: the slots an
// attacker holds can fill it from inside their own private allowances, and the
// validators this node has to reconcile votes with are refused — which is how a
// vote lost to any of the other ceilings gets resent, so the recovery stops
// working under exactly the load it is for. Reserving a share per peer is the
// same answer the scheduling lanes give one channel further in.
func (r *Reactor) allowMaj23Claim(ctx context.Context, envelope *p2p.Envelope, cost int) bool {
	withinShare, err := r.maj23PeerShare.Limit(ctx, envelope.From, 1)
	if err != nil {
		r.logger.Error("majority claim share limiter failed", "peer", envelope.From, "err", err)
		withinShare = true
	}
	if withinShare {
		return r.chargeStateAllowance(ctx, envelope, cost)
	}

	// Look at the surplus before charging the sender. A claim the node as a whole
	// will not answer must not also cost the peer its own allowance, or an honest
	// peer would spend it on asks that are discarded anyway and be throttled
	// hardest just as the channel congests. Claims reach here on the State
	// channel alone, which one goroutine serves, so nothing can take the token
	// between this look and the charge below — and the charge is still the
	// authority, so a second caller would over-admit by one rather than by the
	// difference.
	now := r.clock.Now()
	if r.maj23SurplusLimit.TokensAt(now) < 1 {
		return r.refuseMaj23Claim(envelope)
	}
	if !r.chargeStateAllowance(ctx, envelope, cost) {
		return false
	}
	if !r.maj23SurplusLimit.AllowN(now, 1) {
		return r.refuseMaj23Claim(envelope)
	}
	return true
}

// refuseMaj23Claim records a claim declined over the contended surplus. It
// always reports false, so callers can return it directly.
func (r *Reactor) refuseMaj23Claim(envelope *p2p.Envelope) bool {
	r.Metrics.StateChannelDrops.Add(1)
	r.logger.Debug("dropping majority claim over the contended surplus", "peer", envelope.From)
	return false
}

// sendVoteSetBits hands a VoteSetBits response to the router, giving up if it
// cannot be delivered promptly, and reports whether it went out.
//
// Failing to deliver is never reported to the caller: it says nothing about the
// peer that asked, and the caller turns any error into a peer error. The peer
// asks again on its next gossip tick.
func (r *Reactor) sendVoteSetBits(
	ctx context.Context,
	voteSetCh p2p.Channel,
	to types.NodeID,
	msg *tmcons.VoteSetBits,
) bool {
	sendCtx, cancel := context.WithTimeout(ctx, voteSetResponseTimeout)
	defer cancel()

	if err := voteSetCh.Send(sendCtx, p2p.Envelope{To: to, Message: msg}); err != nil {
		r.logger.Debug("dropping vote set response the router could not take",
			"peer", to, "height", msg.Height, "round", msg.Round, "err", err)
		return false
	}
	return true
}

func (r *Reactor) processMsgCh(ctx context.Context, msgCh p2p.Channel, chBundle channelBundle) {
	iter := msgCh.Receive(ctx)
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if !r.envelopeFromLiveConn(envelope) {
			// An envelope an ended connection left in flight: the peer's earlier
			// connection produced it, it was still queued when that connection went
			// down, and the same NodeID has since reconnected. Drop it as a silent
			// local drop here, before any rate limiter is charged, any proto is
			// parsed, or any peer state is mutated — charging the reconnected peer's
			// budget for it, mutating its gossip state from it, or reporting it as a
			// peer error over a malformed one would all punish the peer for a message
			// its live connection never sent. An ended connection is nobody's fault.
			//
			// This check and the steps that follow it do not share a lock, so a
			// concurrent reconnect (peerDown then peerUp swapping this NodeID's
			// generation) can still slip a stale envelope past here into the limiter
			// charge or the gossip-bit mutation that run next. That residual is
			// bounded and self-inflicted: the reconnected connection is the same
			// NodeID, so the only budget spent or state touched is its own, the
			// amount is one envelope, and the peer-error path is separately guarded
			// below by re-checking liveness before any error is raised — so the
			// window can never report an honest peer. Holding a lock across the
			// limiter and handler to close it would couple this check to the
			// scheduler's lock ordering for no attacker-reachable gain.
			continue
		}
		if !r.allowVoteChannelMessage(ctx, envelope) {
			continue
		}
		if !r.allowDataChannelMessage(ctx, envelope) {
			continue
		}
		if !r.allowStateChannelMessage(ctx, envelope) {
			continue
		}
		if err := r.handleMessage(ctx, envelope, chBundle); err != nil {
			// Never dump the full envelope: it is attacker-controlled and can be
			// large, so echoing it turns a flood of unprocessable messages into a
			// log-amplification vector. Log errors an unprivileged peer can trigger
			// at will at debug; anything not positively matched stays at Error so a
			// real fault is not hidden.
			if isPeerFloodableError(err) {
				r.logger.Debug("rejected peer message", "ch_id", envelope.ChannelID, "peer", envelope.From, "err", err)
			} else {
				r.logger.Error("failed to process message", "ch_id", envelope.ChannelID, "peer", envelope.From, "err", err)
			}
			// Re-check liveness before raising the peer error. The top-of-loop check
			// and the handler do not share a lock, so a reconnect could have swapped
			// this NodeID's connection generation while the handler ran, leaving the
			// error attributable to a connection that has already ended. Reporting it
			// then would blame the peer standing here now for a message its live
			// connection never sent — the one punitive step the bounded window above
			// must not reach. A stale envelope's error is a silent local drop.
			if !r.envelopeFromLiveConn(envelope) {
				continue
			}
			if serr := msgCh.SendError(ctx, p2p.PeerError{
				NodeID: envelope.From,
				Err:    err,
			}); serr != nil {
				return
			}
		}
	}
}

// processPeerUpdates initiates a blocking process where we listen for and handle
// PeerUpdate messages. When the reactor is stopped, we will catch the signal and
// close the p2p PeerUpdatesCh gracefully.
func (r *Reactor) processPeerUpdates(ctx context.Context, peerUpdates *p2p.PeerUpdates, chans channelBundle) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.processPeerUpdate(ctx, peerUpdate, chans)
		}
	}
}

func (r *Reactor) peerStatsRoutine(ctx context.Context, peerUpdates *p2p.PeerUpdates) {
	r.logger.Debug("peerStatsRoutine starting")
	for {
		select {
		case <-ctx.Done():
			r.logger.Trace("stopping peerStatsRoutine due to context cancellation")
			return
		case msg := <-r.state.statsMsgQueue.ch:
			ps, ok := r.GetPeerState(msg.PeerID)
			if !ok || ps == nil {
				// it's quite common to happen when a peer is removed
				r.logger.Trace("attempt to update stats for non-existent peer", "peer", msg.PeerID)
				continue
			}

			switch msg.Msg.(type) {
			case *CommitMessage:
				if numCommits := ps.RecordCommit(); numCommits%commitsToContributeToBecomeGoodPeer == 0 {
					peerUpdates.SendUpdate(ctx, p2p.PeerUpdate{
						NodeID: msg.PeerID,
						Status: p2p.PeerStatusGood,
					})
				}

			case *VoteMessage:
				numVotes := ps.RecordVote()
				if numVotes%votesToContributeToBecomeGoodPeer == 0 {
					peerUpdates.SendUpdate(ctx, p2p.PeerUpdate{
						NodeID: msg.PeerID,
						Status: p2p.PeerStatusGood,
					})
				}

			case *BlockPartMessage:
				numParts := ps.RecordBlockPart()
				if numParts%blocksToContributeToBecomeGoodPeer == 0 {
					peerUpdates.SendUpdate(ctx, p2p.PeerUpdate{
						NodeID: msg.PeerID,
						Status: p2p.PeerStatusGood,
					})
				}
			}
		}
	}
}

func (r *Reactor) peerErrorRoutine(ctx context.Context, msgCh p2p.Channel) {
	r.logger.Debug("peerErrorRoutine starting")
	for {
		select {
		case <-ctx.Done():
			r.logger.Trace("stopping peerErrorRoutine due to context cancellation")
			return
		case peerErr := <-r.state.peerErrorQueue.ch:
			ps, ok := r.GetPeerState(peerErr.PeerID)
			if !ok || ps == nil {
				// peer may have been removed already
				r.logger.Trace("attempt to report error for non-existent peer", "peer", peerErr.PeerID)
				continue
			}

			// Send fatal peer error through the channel
			if err := msgCh.SendError(ctx, p2p.PeerError{
				NodeID: peerErr.PeerID,
				Err:    peerErr.Err,
				Fatal:  peerErr.Fatal,
			}); err != nil {
				r.logger.Trace("failed to send peer error", "peer", peerErr.PeerID, "err", err)
			}
		}
	}
}

func (r *Reactor) GetConsensusState() *State {
	return r.state
}

func (r *Reactor) SetStateSyncingMetrics(v float64) {
	r.Metrics.StateSyncing.Set(v)
}

func (r *Reactor) SetBlockSyncingMetrics(v float64) {
	r.Metrics.BlockSyncing.Set(v)
}
