package consensus

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// proposerHeartbeatEventName is emitted with each heartbeat this node signs, for
// the reactor to broadcast.
const proposerHeartbeatEventName = "proposerHeartbeat"

// errProposerHeartbeatWrongProposer is returned for a heartbeat signed in the name
// of a validator that is not the proposer of the round it names.
var errProposerHeartbeatWrongProposer = errors.New("proposer heartbeat is not from the proposer of its round")

// proposerSilence reports that the proposer of a round this node waits in sent
// no heartbeat for as long as the round allows.
type proposerSilence struct {
	Height   int64
	Round    int32
	Proposer types.ProTxHash
	// arm identifies the silence timer that raised the report, so a heartbeat
	// that re-armed the watch since can be told apart (see current).
	arm uint64
}

// livenessTarget is the round whose proposer the node watches or, when the node
// is that proposer, announces.
type livenessTarget struct {
	height     int64
	round      int32
	proposer   *types.Validator
	chainID    string
	quorumType btcjson.LLMQType
	quorumHash crypto.QuorumHash
	// interval is how often the proposer sends a heartbeat.
	interval time.Duration
	// silence is how long the watch waits without one.
	silence time.Duration
}

func (t *livenessTarget) is(height int64, round int32) bool {
	return t != nil && t.height == height && t.round == round
}

// relayRounds is what a node needs to check a heartbeat for a round of its
// height that it is not waiting in, so it can pass the heartbeat on to the
// validators that are: validators move between rounds at different times, and
// connections between them are not a full mesh.
type relayRounds struct {
	height     int64
	chainID    string
	quorumType btcjson.LLMQType
	quorumHash crypto.QuorumHash
	timeouts   types.TimeoutParams
	// first and last bound the rounds held: those within
	// maxRoundSkipRoundsAhead of the node's (relayWindow).
	first, last int32
	// proposers holds the proposer of each of those rounds.
	proposers map[int32]*types.Validator
}

// relayWindow returns the rounds whose heartbeats a node in round relays.
func relayWindow(round int32) (first, last int32) {
	return max(round-maxRoundSkipRoundsAhead, 0), round + maxRoundSkipRoundsAhead
}

func (r *relayRounds) proposerOf(height int64, round int32) *types.Validator {
	if r == nil || r.height != height {
		return nil
	}
	return r.proposers[round]
}

// proposerLiveness keeps track of whether the proposer of the round the node is
// waiting in is still there.
//
// While the node waits for transactions in a round of a CONSENSUS_VERSION_2
// chain, it either is the proposer of that round, and then sends a signed
// heartbeat every interval, or watches that proposer's heartbeats and reports
// the proposer as silent once none has arrived for the round's silence timeout.
//
// The consensus goroutine decides what to do (reconcile) and acts on a silence;
// the reactor feeds in heartbeats from peers (heard). Neither touches consensus
// state from here: a heartbeat only resets a timer.
type proposerLiveness struct {
	logger  log.Logger
	metrics *Metrics
	emitter *eventemitter.EventEmitter
	// ready reports whether this node could propose at a height right now. A
	// proposer that could not sends no heartbeat, so a node whose application
	// is down, or that is still catching up, is not waited on.
	ready func(ctx context.Context, height int64) error
	// signerUnsupported is set once this node's signer refused a heartbeat as
	// a request it cannot sign at all. No heartbeat is attempted after that.
	signerUnsupported atomic.Bool
	// budget is the node-wide verification budget a peer's heartbeat draws on
	// before its signature is checked.
	budget types.VerificationBudget

	mtx        sync.Mutex
	target     *livenessTarget
	announcing bool
	// generation changes with every new target, so a sender left from an
	// earlier one can tell it is stale.
	generation uint64
	// arm changes every time the silence timer is armed or stopped, so a timer
	// callback that already fired, or a report it raised, can tell a heartbeat
	// re-armed the watch since.
	arm uint64
	// lastSeq is the sequence of the last heartbeat this node sent for target.
	lastSeq uint64
	// relay holds the rounds near the node's whose heartbeats it passes on.
	relay *relayRounds
	// seen holds the heartbeats of the height accepted, or sent, within the
	// silence timeout, by their signing time. A copy of one is relayed by
	// every peer and counts only once; anything older fails the age check.
	seen     map[heartbeatID]time.Time
	timer    *time.Timer
	cancel   context.CancelFunc
	silentCh chan proposerSilence
}

// heartbeatID tells one heartbeat from another. The proposer signs all four
// fields, and no two heartbeats it signs for a round share sequence and
// timestamp, whether or not it restarted or its clock went back in between.
type heartbeatID struct {
	height    int64
	round     int32
	sequence  uint64
	timestamp int64
}

func heartbeatIDOf(hb *types.ProposerHeartbeat) heartbeatID {
	return heartbeatID{height: hb.Height, round: hb.Round, sequence: hb.Sequence, timestamp: hb.Timestamp.UnixNano()}
}

// maxSeenHeartbeats bounds seen. An honest proposer sends silence/interval
// heartbeats within the silence timeout; the bound only matters against a
// proposer that signs far more, which can keep itself alive anyway.
const maxSeenHeartbeats = 256

func newProposerLiveness(
	logger log.Logger,
	metrics *Metrics,
	emitter *eventemitter.EventEmitter,
	ready func(ctx context.Context, height int64) error,
	budget types.VerificationBudget,
) *proposerLiveness {
	return &proposerLiveness{
		logger:   logger,
		metrics:  metrics,
		emitter:  emitter,
		ready:    ready,
		budget:   budget,
		silentCh: make(chan proposerSilence, 1),
	}
}

// silent delivers a proposerSilence when the watched proposer stops sending
// heartbeats. An event can be stale by the time it is read, so the reader checks
// it against the round it is in.
func (l *proposerLiveness) silent() <-chan proposerSilence {
	return l.silentCh
}

// rearm starts the silence timer again for the watch that raised silence, if
// nothing has re-armed it since. A validator that could not act on a silence,
// such as one whose signer failed for a moment, is told again after another
// silence timeout instead of never.
func (l *proposerLiveness) rearm(silence proposerSilence) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	if silence.arm == l.arm && !l.announcing && l.target.is(silence.Height, silence.Round) {
		l.armLocked()
	}
}

// current reports whether silence is still the latest word on its proposer:
// the timer that raised it has not been re-armed by a heartbeat, or stopped,
// since. A report read after a heartbeat arrived is stale even though it names
// the round the node is in.
func (l *proposerLiveness) current(silence proposerSilence) bool {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return silence.arm == l.arm && !l.announcing && l.target.is(silence.Height, silence.Round)
}

// roundSkipping reports whether the chain skips the rounds of silent proposers:
// from CONSENSUS_VERSION_2, and only where a round skip certificate can prove
// the voting power the validator set needs (see types.RoundSkipsProvable).
func roundSkipping(stateData *StateData) bool {
	return stateData.state.ConsensusParams.Version.ProposerLivenessEnabled() &&
		types.RoundSkipsProvable(stateData.Validators)
}

// waitingRound reports whether the node is waiting for transactions in a round
// whose proposer's liveness it tracks.
func waitingRound(stateData *StateData) bool {
	return roundSkipping(stateData) &&
		stateData.Step == cstypes.RoundStepNewRound &&
		stateData.Proposal == nil &&
		stateData.Commit == nil
}

// followRounds records the proposers of the rounds near the node's, whose
// heartbeats it checks and relays even when it does not wait in their round.
// It runs on the consensus goroutine, like reconcile.
func (l *proposerLiveness) followRounds(stateData *StateData) {
	if !roundSkipping(stateData) || stateData.ProposerSelector == nil {
		l.mtx.Lock()
		l.relay = nil
		l.mtx.Unlock()
		return
	}
	first, last := relayWindow(stateData.Round)
	l.mtx.Lock()
	relay := l.relay
	l.mtx.Unlock()
	if relay != nil && relay.height == stateData.Height && relay.first == first && relay.last == last &&
		bytes.Equal(relay.quorumHash, stateData.Validators.QuorumHash) {
		return
	}

	// Looking up another round moves the selector, so a copy does it.
	selector := stateData.ProposerSelector.Copy()
	relay = &relayRounds{
		height:     stateData.Height,
		chainID:    stateData.state.ChainID,
		quorumType: stateData.Validators.QuorumType,
		quorumHash: stateData.Validators.QuorumHash,
		timeouts:   stateData.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults(),
		first:      first,
		last:       last,
		proposers:  make(map[int32]*types.Validator),
	}
	for round := first; round <= last; round++ {
		proposer, err := selector.GetProposer(stateData.Height, round)
		if err != nil {
			l.logger.Error("cannot determine the proposer of a nearby round", "height", stateData.Height,
				"round", round, "err", err)
			continue
		}
		relay.proposers[round] = proposer.Copy()
	}

	l.mtx.Lock()
	defer l.mtx.Unlock()
	if l.relay == nil || l.relay.height != relay.height {
		l.seen = nil
	}
	l.relay = relay
}

// reconcile starts or stops the heartbeat sender and the proposer watch to match
// the round the node is in. It runs on the consensus goroutine after every input
// the state machine handles.
func (l *proposerLiveness) reconcile(ctx context.Context, stateData *StateData, pv privValidator) {
	if !waitingRound(stateData) {
		l.stop()
		return
	}

	l.mtx.Lock()
	running := l.target.is(stateData.Height, stateData.Round)
	l.mtx.Unlock()
	if running {
		return
	}

	proposer, err := stateData.ProposerSelector.GetProposer(stateData.Height, stateData.Round)
	if err != nil {
		l.logger.Error("cannot determine the proposer to watch", "height", stateData.Height,
			"round", stateData.Round, "err", err)
		l.stop()
		return
	}
	timeouts := stateData.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults()
	target := livenessTarget{
		height: stateData.Height,
		round:  stateData.Round,
		// A copy, since the reactor reads it while the consensus goroutine
		// moves on.
		proposer:   proposer.Copy(),
		chainID:    stateData.state.ChainID,
		quorumType: stateData.Validators.QuorumType,
		quorumHash: stateData.Validators.QuorumHash,
		interval:   timeouts.ProposerHeartbeat,
		silence:    timeouts.ProposerSilenceTimeout(stateData.Round),
	}
	if !pv.IsZero() && pv.IsProTxHashEqual(proposer.ProTxHash) {
		l.announce(ctx, target, pv)
		return
	}
	l.watch(target)
}

// watch starts waiting for heartbeats from target's proposer. The first one is
// due within the silence timeout too: the proposer entered the round at about
// the same time as this node.
func (l *proposerLiveness) watch(target livenessTarget) {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	l.resetLocked(&target)
	l.armLocked()
}

// announce starts sending heartbeats for target, whose proposer this node is.
func (l *proposerLiveness) announce(ctx context.Context, target livenessTarget, pv privValidator) {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	l.resetLocked(&target)
	l.announcing = true
	// A signer that cannot sign heartbeats at all is not asked again every
	// interval; the round is handed on once validators stop hearing from it.
	if l.signerUnsupported.Load() {
		return
	}
	sendCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	go l.sendHeartbeats(sendCtx, l.generation, target, pv)
}

// stop ends any watch or heartbeat sender.
func (l *proposerLiveness) stop() {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	if l.target == nil {
		return
	}
	l.resetLocked(nil)
}

func (l *proposerLiveness) resetLocked(target *livenessTarget) {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.generation++
	l.arm++
	l.target = target
	l.announcing = false
	l.lastSeq = 0
}

// rememberLocked records a heartbeat as seen, and forgets those signed longer
// than the silence timeout before now, which the age check refuses anyway.
func (l *proposerLiveness) rememberLocked(id heartbeatID, signed time.Time, now time.Time, silence time.Duration) {
	if l.seen == nil {
		l.seen = make(map[heartbeatID]time.Time)
	}
	for seenID, at := range l.seen {
		if now.Sub(at) > silence {
			delete(l.seen, seenID)
		}
	}
	if len(l.seen) >= maxSeenHeartbeats {
		var oldestID heartbeatID
		var oldest time.Time
		for seenID, at := range l.seen {
			if oldest.IsZero() || at.Before(oldest) {
				oldestID, oldest = seenID, at
			}
		}
		delete(l.seen, oldestID)
	}
	l.seen[id] = signed
}

// armLocked (re)starts the silence timer for the current target.
func (l *proposerLiveness) armLocked() {
	if l.timer != nil {
		l.timer.Stop()
	}
	// Stop does not hold back a callback that already fired and waits for the
	// lock, so the callback checks that its arm is still the current one.
	l.arm++
	arm := l.arm
	l.timer = time.AfterFunc(l.target.silence, func() { l.fire(arm) })
}

func (l *proposerLiveness) fire(arm uint64) {
	l.mtx.Lock()
	if arm != l.arm || l.target == nil || l.announcing {
		l.mtx.Unlock()
		return
	}
	event := proposerSilence{
		Height:   l.target.height,
		Round:    l.target.round,
		Proposer: l.target.proposer.ProTxHash,
		arm:      arm,
	}
	l.mtx.Unlock()

	// The channel holds one event. One still unread is older than this and
	// would be dropped by the reader anyway, so it gives way.
	for {
		select {
		case l.silentCh <- event:
			return
		default:
		}
		select {
		case <-l.silentCh:
		default:
		}
	}
}

// heard takes a heartbeat from a peer. It reports whether the heartbeat is new
// and genuine and should be relayed to other peers, and returns an error only
// for one no honest peer would have sent.
//
// It runs on the reactor's goroutine. The cheap checks come first, so the only
// heartbeats verified are those from the proposer of their round, signed
// recently, and not seen before. A heartbeat for the round the node waits in
// refreshes its watch; one for a nearby round of its height is only relayed,
// for the validators still, or already, waiting there.
//
// Heartbeats are not ordered. Any recent one not seen before is news that the
// proposer is there, whatever its sequence: a proposer that restarts, or whose
// clock goes back, signs heartbeats whose sequence can be lower than one sent
// before, and is still there.
func (l *proposerLiveness) heard(hb *types.ProposerHeartbeat) (bool, error) {
	id := heartbeatIDOf(hb)
	l.mtx.Lock()
	target, generation, relay := l.target, l.generation, l.relay
	_, seen := l.seen[id]
	l.mtx.Unlock()

	var (
		proposer   *types.Validator
		chainID    string
		quorumType btcjson.LLMQType
		quorumHash crypto.QuorumHash
		silence    time.Duration
	)
	watched := target.is(hb.Height, hb.Round)
	switch {
	case watched:
		proposer, chainID, quorumType, quorumHash, silence =
			target.proposer, target.chainID, target.quorumType, target.quorumHash, target.silence
	case relay.proposerOf(hb.Height, hb.Round) != nil:
		proposer, chainID, quorumType, quorumHash = relay.proposerOf(hb.Height, hb.Round),
			relay.chainID, relay.quorumType, relay.quorumHash
		silence = relay.timeouts.ProposerSilenceTimeout(hb.Round)
	default:
		// A heartbeat for a round too far from this node's says nothing wrong
		// about its sender: rounds move at different times on different nodes.
		return false, nil
	}
	if !proposer.ProTxHash.Equal(hb.ProposerProTxHash) {
		return false, errProposerHeartbeatWrongProposer
	}
	// Copies arrive from every peer that relays one; only the first counts.
	if seen {
		return false, nil
	}
	// A heartbeat signed long ago says nothing about the proposer now, whoever
	// relays it.
	now := tmtime.Now()
	if age := now.Sub(hb.Timestamp); age > silence || age < -silence {
		return false, nil
	}
	// A node outside the validator set does not hold the members' key shares
	// and cannot check the signature, so it neither tracks nor relays.
	if proposer.PubKey == nil {
		return false, nil
	}
	// The check below is a signature verification like a vote's, so it draws
	// on the node-wide budget. One this node cannot afford now is dropped: the
	// next heartbeat comes within the interval, from this peer or another.
	if l.budget != nil && !l.budget.Allow(baseMessageCost) {
		return false, nil
	}
	if err := hb.Verify(chainID, quorumType, quorumHash, proposer.PubKey); err != nil {
		return false, err
	}

	l.mtx.Lock()
	defer l.mtx.Unlock()
	if _, seen := l.seen[id]; seen {
		return false, nil
	}
	l.rememberLocked(id, hb.Timestamp, now, silence)
	if watched && generation == l.generation {
		if !l.announcing {
			l.armLocked()
		}
		l.metrics.ProposerHeartbeatsReceived.Add(1)
	}
	return true, nil
}

// heartbeatSequence returns the sequence of a heartbeat signed at now, after
// one with sequence last: the signing time in milliseconds, or one more than
// last if the clock has not moved past it. Within one run of the proposer the
// sequence only grows, so each heartbeat is a signing request of its own;
// counting from the clock keeps a restarted proposer clear of the sequences it
// used before.
func heartbeatSequence(last uint64, now time.Time) uint64 {
	ms := now.UnixMilli()
	if ms <= 0 {
		return last + 1
	}
	return max(last+1, uint64(ms))
}

func (l *proposerLiveness) sendHeartbeats(ctx context.Context, generation uint64, target livenessTarget, pv privValidator) {
	ticker := time.NewTicker(target.interval)
	defer ticker.Stop()

	for {
		if !l.sendHeartbeat(ctx, generation, target, pv) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sendHeartbeat signs and emits one heartbeat for target, if this node could
// propose now. It reports whether more should follow: not once the signer
// turns out unable to sign heartbeats at all.
func (l *proposerLiveness) sendHeartbeat(
	ctx context.Context,
	generation uint64,
	target livenessTarget,
	pv privValidator,
) bool {
	ctx, cancel := context.WithTimeout(ctx, target.interval)
	defer cancel()

	if l.ready != nil {
		if err := l.ready(ctx, target.height); err != nil {
			l.logger.Debug("not sending a proposer heartbeat: this node cannot propose right now",
				"height", target.height, "round", target.round, "err", err)
			return true
		}
	}

	now := tmtime.Now()
	l.mtx.Lock()
	if generation != l.generation {
		l.mtx.Unlock()
		return false
	}
	l.lastSeq = heartbeatSequence(l.lastSeq, now)
	sequence := l.lastSeq
	l.mtx.Unlock()

	pb := &tmproto.ProposerHeartbeat{
		Height:            target.height,
		Round:             target.round,
		Sequence:          sequence,
		ProposerProTxHash: pv.ProTxHash,
		Timestamp:         now,
	}
	if err := pv.SignProposerHeartbeat(ctx, target.chainID, target.quorumType, target.quorumHash, pb); err != nil {
		if errors.Is(err, types.ErrSigningNotSupported) {
			if !l.signerUnsupported.Swap(true) {
				l.logger.Info("this node's signer cannot sign proposer heartbeats, so it sends none; "+
					"validators hand on its rounds that wait for transactions", "err", err)
			}
			return false
		}
		if ctx.Err() == nil {
			l.logger.Error("cannot sign proposer heartbeat", "height", target.height, "round", target.round, "err", err)
		}
		return true
	}
	hb, err := types.ProposerHeartbeatFromProto(pb)
	if err != nil {
		l.logger.Error("signed an invalid proposer heartbeat", "heartbeat", hb, "err", err)
		return true
	}
	// Peers relay it back; it is not news to this node.
	l.mtx.Lock()
	if generation == l.generation {
		l.rememberLocked(heartbeatIDOf(hb), hb.Timestamp, now, target.silence)
	}
	l.mtx.Unlock()
	l.metrics.ProposerHeartbeatsSent.Add(1)
	l.emitter.Emit(proposerHeartbeatEventName, hb)
	return true
}
