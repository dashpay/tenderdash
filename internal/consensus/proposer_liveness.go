package consensus

import (
	"context"
	"errors"
	"sync"
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
	// ready reports whether this node could propose right now. A proposer that
	// could not sends no heartbeat, so a node whose application is down is not
	// waited on.
	ready func(context.Context) error
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
	// seen holds the heartbeats for target accepted, or sent, within the
	// silence timeout, by their signing time. A copy of one is relayed by
	// every peer and counts only once; anything older fails the age check.
	seen     map[heartbeatID]time.Time
	timer    *time.Timer
	cancel   context.CancelFunc
	silentCh chan proposerSilence
}

// heartbeatID tells one heartbeat of a round from another. The proposer signs
// both fields, and no two heartbeats it signs share them, whether or not it
// restarted or its clock went back in between.
type heartbeatID struct {
	sequence  uint64
	timestamp int64
}

func heartbeatIDOf(hb *types.ProposerHeartbeat) heartbeatID {
	return heartbeatID{sequence: hb.Sequence, timestamp: hb.Timestamp.UnixNano()}
}

// maxSeenHeartbeats bounds seen. An honest proposer sends silence/interval
// heartbeats within the silence timeout; the bound only matters against a
// proposer that signs far more, which can keep itself alive anyway.
const maxSeenHeartbeats = 256

func newProposerLiveness(
	logger log.Logger,
	metrics *Metrics,
	emitter *eventemitter.EventEmitter,
	ready func(context.Context) error,
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

// current reports whether silence is still the latest word on its proposer:
// the timer that raised it has not been re-armed by a heartbeat, or stopped,
// since. A report read after a heartbeat arrived is stale even though it names
// the round the node is in.
func (l *proposerLiveness) current(silence proposerSilence) bool {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return silence.arm == l.arm && !l.announcing && l.target.is(silence.Height, silence.Round)
}

// waitingRound reports whether the node is waiting for transactions in a round
// whose proposer's liveness it tracks.
func waitingRound(stateData *StateData) bool {
	return stateData.state.ConsensusParams.Version.ProposerLivenessEnabled() &&
		stateData.Step == cstypes.RoundStepNewRound &&
		stateData.Proposal == nil &&
		stateData.Commit == nil
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
	l.seen = nil
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
// heartbeats verified are those that would refresh the watch: from the proposer
// of the round being waited in, signed recently, and not seen before.
//
// Heartbeats are not ordered. Any recent one not seen before is news that the
// proposer is there, whatever its sequence: a proposer that restarts, or whose
// clock goes back, signs heartbeats whose sequence can be lower than one sent
// before, and is still there.
func (l *proposerLiveness) heard(hb *types.ProposerHeartbeat) (bool, error) {
	id := heartbeatIDOf(hb)
	l.mtx.Lock()
	target := l.target
	generation := l.generation
	_, seen := l.seen[id]
	l.mtx.Unlock()

	// A heartbeat for another round is no use to a node that is not waiting in
	// it, and says nothing wrong about the sender: rounds move at different
	// times on different nodes.
	if !target.is(hb.Height, hb.Round) {
		return false, nil
	}
	if !target.proposer.ProTxHash.Equal(hb.ProposerProTxHash) {
		return false, errProposerHeartbeatWrongProposer
	}
	// Copies arrive from every peer that relays one; only the first counts.
	if seen {
		return false, nil
	}
	// A heartbeat signed long ago says nothing about the proposer now, whoever
	// relays it.
	now := tmtime.Now()
	if age := now.Sub(hb.Timestamp); age > target.silence || age < -target.silence {
		return false, nil
	}
	// A node outside the validator set does not hold the members' key shares
	// and cannot check the signature, so it neither tracks nor relays.
	if target.proposer.PubKey == nil {
		return false, nil
	}
	// The check below is a signature verification like a vote's, so it draws
	// on the node-wide budget. One this node cannot afford now is dropped: the
	// next heartbeat comes within the interval, from this peer or another.
	if l.budget != nil && !l.budget.Allow(baseMessageCost) {
		return false, nil
	}
	if err := hb.Verify(target.chainID, target.quorumType, target.quorumHash, target.proposer.PubKey); err != nil {
		return false, err
	}

	l.mtx.Lock()
	defer l.mtx.Unlock()
	if _, seen := l.seen[id]; generation != l.generation || seen {
		return false, nil
	}
	l.rememberLocked(id, hb.Timestamp, now, target.silence)
	if !l.announcing {
		l.armLocked()
	}
	l.metrics.ProposerHeartbeatsReceived.Add(1)
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
		l.sendHeartbeat(ctx, generation, target, pv)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (l *proposerLiveness) sendHeartbeat(ctx context.Context, generation uint64, target livenessTarget, pv privValidator) {
	ctx, cancel := context.WithTimeout(ctx, target.interval)
	defer cancel()

	if l.ready != nil {
		if err := l.ready(ctx); err != nil {
			l.logger.Debug("not sending a proposer heartbeat: this node cannot propose right now",
				"height", target.height, "round", target.round, "err", err)
			return
		}
	}

	now := tmtime.Now()
	l.mtx.Lock()
	if generation != l.generation {
		l.mtx.Unlock()
		return
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
		if ctx.Err() == nil {
			l.logger.Error("cannot sign proposer heartbeat", "height", target.height, "round", target.round, "err", err)
		}
		return
	}
	hb, err := types.ProposerHeartbeatFromProto(pb)
	if err != nil {
		l.logger.Error("signed an invalid proposer heartbeat", "heartbeat", hb, "err", err)
		return
	}
	// Peers relay it back; it is not news to this node.
	l.mtx.Lock()
	if generation == l.generation {
		l.rememberLocked(heartbeatIDOf(hb), hb.Timestamp, now, target.silence)
	}
	l.mtx.Unlock()
	l.metrics.ProposerHeartbeatsSent.Add(1)
	l.emitter.Emit(proposerHeartbeatEventName, hb)
}
