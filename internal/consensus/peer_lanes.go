package consensus

import (
	"context"
	"sync"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/jonboulle/clockwork"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

const (
	// laneCapacity is how many messages one peer may have waiting for the
	// consensus goroutine.
	//
	// It is sized from what a single peer legitimately delivers in one burst,
	// because the peer that reaches capacity under load is the honest one at
	// full stretch, not the attacker: a peer's own rate limiter caps what it can
	// front-load long before the lane does.
	//
	// The binding case is a node catching up from ONE peer, which must still
	// receive everything that peer sends it:
	//   - a whole proposal block at the default 21 MB maximum arrives as 337
	//     64 kB parts, all from the peer we are syncing from;
	//   - the per-peer vote-channel bucket admits at most voteRateBurst work
	//     units in an instant, so at most that many of the cheapest messages.
	// 512 covers both with margin.
	//
	// It does not raise the memory ceiling: the lanes are bounded in aggregate,
	// in messages by what the single queue they replace held and in payload by
	// laneByteCapacity. Both aggregates are smaller than every lane's capacity
	// added up, so once several peers are sending hard it is an aggregate that
	// binds and a single peer can no longer hold a whole block's parts. What
	// follows is a round that times out and starts again — the price of bounding
	// the memory a flood can pin.
	laneCapacity = 512

	// laneQuantum is the verification work one lane is granted per turn of the
	// rotation.
	//
	// One work unit — the finest the cost model can express — is deliberate. A
	// lane's head is served once its lane has been granted the head's cost, so
	// while it waits every other lane may complete a quantum's worth of work per
	// turn: the work a peer can push in front of an honest message of cost W is
	// therefore bounded by lanes x max(quantum, W). Granting more per turn buys
	// nothing for the lane being served and multiplies what every other lane may
	// spend ahead of it.
	//
	// The bound scales with the head's own cost, which is the number an operator
	// needs from it: with every connection slot taken and sending, the heaviest
	// precommit Dash validators produce waits on the order of a couple of
	// seconds — inside the propose timeout, outside the vote timeout. A
	// protocol-maximum head is outside both, and is affordable only on a node
	// that is not under load.
	laneQuantum = baseMessageCost

	// laneByteCapacity is how much message payload all lanes together may hold.
	//
	// Counting messages does not bound memory. A block part is two orders of
	// magnitude larger than a vote, so a backlog bounded only in messages is a
	// backlog of block parts bounded by the message count times the largest
	// message — over a gigabyte at the counts the lanes allow, which is memory a
	// peer can make this node pin by sending faster than it is served.
	//
	// It is sized from what the node legitimately has in flight rather than from
	// the message count: three maximum-size proposal blocks' worth of parts,
	// which covers a lane catching up on a whole block while others gossip. A
	// backlog of votes never reaches it, so for those the message count remains
	// the bound that binds.
	laneByteCapacity = 64 << 20

	// laneMessageOverhead is what a message costs against that ceiling before
	// its payload. Everything other than a block part is small and of roughly
	// this order, so charging them a flat amount keeps the accounting to one
	// addition without letting a backlog of them go unmeasured.
	laneMessageOverhead = 1 << 10

	// laneVoteExtensionOverhead is charged for each vote extension a vote or
	// commit carries, on top of the extension's own wire bytes.
	//
	// A message is held in a lane as deserialized Go objects, not as the wire
	// form proto.Size measures. Each vote extension becomes a separate heap
	// allocation: a struct with several byte-slice fields, each carrying a
	// slice header, reached through a pointer in the container slice — fixed
	// per-object memory that never appears on the wire. proto.Size therefore
	// undercounts a message of many near-empty extensions, letting it retain
	// several times its wire size in heap while the accounting sees it as tiny.
	// Charging a fixed amount per extension makes the byte ceiling an upper bound
	// on the heap actually retained. The count is capped at MaxVoteExtensions, so
	// the surcharge is bounded; 512 bytes covers one extension's struct, its slice
	// headers, the container pointer, and allocator rounding with room to spare.
	laneVoteExtensionOverhead = 512

	// laneProofAuntOverhead is charged for each Merkle-proof aunt a block part
	// carries. An aunt is a hash held in a slice of byte slices: a slice header
	// and a container pointer the wire form does not carry. Aunts are capped at
	// MaxAunts and a legitimate part carries only a tree-depth of them, so at 64
	// bytes each the surcharge leaves the byte ceiling's three-block sizing intact
	// while still upper-bounding the heap of a part padded with aunts to the limit.
	laneProofAuntOverhead = 64

	// laneAllocatorSlackNum and laneAllocatorSlackDen scale a message's marshaled
	// payload up to a strict upper bound on the heap the Go allocator rounds it to;
	// see roundedPayloadBytes.
	laneAllocatorSlackNum = 13
	laneAllocatorSlackDen = 10

	// laneIdleTimeout is how long a lane with nothing queued is kept before it
	// is reclaimed.
	//
	// Lanes are purged when a peer disconnects, but that runs concurrently with
	// the delivery of the peer's last messages, so a lane can be recreated just
	// after its peer is gone. Such a lane is served like any other and then
	// reclaimed here, which is what bounds the state an attacker cycling through
	// free node identities can leave behind.
	laneIdleTimeout = 60 * time.Second
)

// queuedMsg is a queued message paired with the byte charge computed for it when
// it was enqueued. The charge is stored rather than recomputed so that the byte
// accounting cannot drift: enqueue and every later pop or shed debit exactly what
// enqueue credited, whatever happens to the message in between.
type queuedMsg struct {
	msgInfo
	bytes int
}

// peerLane holds the messages one peer has waiting, and that peer's standing in
// the rotation.
type peerLane struct {
	queue []queuedMsg
	// bytes is the payload this lane holds, which is what the shared byte
	// ceiling is accounted in.
	bytes int
	// deficit is the verification work this lane has been granted and not yet
	// spent. It is what lets a lane save up for a head more expensive than one
	// quantum instead of being overtaken by cheaper messages forever.
	deficit int
	// turnOpen reports whether this lane's current turn has already been granted
	// its quantum, so a lane cannot collect two grants from one turn.
	turnOpen bool
	// lastActive is when the lane last carried traffic, and dates it for
	// reclamation once it falls idle.
	lastActive time.Time
}

// active reports whether the lane holds messages, which is exactly when it takes
// part in the rotation.
func (l *peerLane) active() bool {
	return len(l.queue) > 0
}

// peerLanes schedules the messages peers send to the consensus state, giving
// each peer a lane of its own and serving the lanes by deficit round robin.
//
// One arrival-ordered queue shared by every peer lets whoever sends most be
// served most, so a peer flooding cheap messages can hold up a message the node
// needs to make progress. Serving per-peer lanes in rotation bounds that: a
// continuously connected peer's head is served once the other lanes have had a
// turn each, whatever they send.
//
// Round robin alone is not enough, because messages differ in cost by up to two
// orders of magnitude; the deficit counter is what makes the rotation fair in
// verification work rather than in message count, so cheap messages cannot
// overtake an expensive one indefinitely.
//
// It replaces the peer queue's reader rather than adding a stage: the same
// goroutine that drained the queue now runs the rotation, so no verification
// moves off the consensus goroutine.
type peerLanes struct {
	waiter budgetWaiter
	// budgetSaturation reports how full the verification budget is, for the
	// saturation gauge. It is the same object as waiter when that budget can
	// report it, and nil otherwise.
	budgetSaturation budgetSaturationReporter
	clock            clockwork.Clock
	logger           log.Logger
	metrics          *Metrics

	// bufferCapacity is how many messages all lanes together may hold. It is the
	// size of the single shared queue the lanes replace, so per-peer scheduling
	// does not raise how many messages a flood can queue up.
	bufferCapacity int

	// byteCapacity is how much payload all lanes together may hold. It is the
	// bound that binds for block parts, where the message count alone would
	// leave the memory a flood can pin a thousand times larger than what the
	// node has any use for.
	byteCapacity int

	// mtx guards everything below it: lanes are filled by the reactor's channel
	// goroutines and drained by the scheduler.
	mtx   sync.Mutex
	lanes map[types.NodeID]*peerLane
	// rotation is the logical service order of the active lanes: the lane at the
	// front (index 0) is the one whose turn it is, and a lane that activates
	// joins at the back. The order is by lane identity, not by any lane's
	// physical position, so a lane leaving and rejoining moves to the back and
	// cannot deny a waiting lane its turn — which is what bounds how long a
	// continuously backlogged lane waits however other lanes churn.
	rotation []types.NodeID
	queued   int
	// queuedBytes is the payload all lanes hold together.
	queuedBytes int
	lastGC      time.Time

	// sessions is the live connection session of each currently admitted peer.
	// A lane may be created or added to only for a message whose session is the
	// peer's live one, so a message left in flight by a session that has already
	// ended — a peer that disconnected, or an earlier connection of one that
	// reconnected — cannot create or revive a lane and push the active-lane count
	// past the connection slots the node accepts.
	sessions    map[types.NodeID]uint64
	nextSession uint64

	// ready wakes the scheduler when a message arrives while it has nothing to
	// serve.
	ready chan struct{}

	// settled is signaled once the consensus goroutine has finished with a
	// message this scheduler handed it.
	//
	// The scheduler makes room in the verification budget for a message before
	// dispatching it, which is only sound if what it reads from the budget
	// includes everything already dispatched. The handoff is unbuffered, so it
	// completes when the consensus goroutine *starts* receiving; without this
	// signal the scheduler would look at the budget while the previous message
	// still had its verification — and its charges — ahead of it.
	settled chan struct{}
	// outstanding reports whether a dispatched message has yet to settle. Only
	// the scheduler goroutine touches it, and there is only ever one: the
	// message queue's reader.
	outstanding bool
}

// peerLanesOptionFunc overrides a default parameter of peerLanes.
type peerLanesOptionFunc func(*peerLanes)

// withLaneBudget gives the scheduler the verification budget it makes room in
// before dispatching a message. A nil budget disables rate limiting and the
// scheduler never waits.
//
// A non-nil budget must be able to defer a message until its whole cost is
// affordable — see budgetCanWait for why. A budget that cannot wait is rejected
// at construction by the caller (which checks budgetCanWait and returns an
// error); here it simply leaves the scheduler without a waiter, so a budget that
// slipped through would disable waiting rather than take effect. The construction
// check is what keeps that from happening silently.
func withLaneBudget(budget types.VerificationBudget) peerLanesOptionFunc {
	return func(l *peerLanes) {
		if budget == nil {
			return
		}
		waiter, ok := budget.(budgetWaiter)
		if !ok {
			return
		}
		l.waiter = waiter
		l.budgetSaturation, _ = budget.(budgetSaturationReporter)
	}
}

// budgetCanWait reports whether a verification budget can defer a message until
// its whole cost is affordable, which the scheduler requires.
//
// The scheduler makes room for a message's whole cost before dispatching it and
// the message's staged verification then draws that cost; a budget that could
// only decide affordability without waiting would let the scheduler admit a
// message the budget cannot in fact cover, so the message pays for part of its
// verification and is dropped after — losing the whole-message atomicity the
// scheduler exists to guarantee. The bundled budget can wait; a replacement
// supplied through WithVerificationBudget must too. The check is the caller's, so
// an incompatible budget is a construction error rather than a later panic.
func budgetCanWait(budget types.VerificationBudget) bool {
	if budget == nil {
		return true
	}
	_, ok := budget.(budgetWaiter)
	return ok
}

// withLaneClock sets the time source lane reclamation is dated against. The
// default is the wall clock; a test injects a fake clock to advance time
// explicitly.
//
// It does not reach the verification budget, which is metered against a clock
// of its own, so a test that wants to control how long a message waits for
// budget must give the same clock to both.
func withLaneClock(clock clockwork.Clock) peerLanesOptionFunc {
	return func(l *peerLanes) {
		l.clock = clock
	}
}

func withLaneLogger(logger log.Logger) peerLanesOptionFunc {
	return func(l *peerLanes) {
		l.logger = logger
	}
}

func withLaneMetrics(metrics *Metrics) peerLanesOptionFunc {
	return func(l *peerLanes) {
		l.metrics = metrics
	}
}

// withLaneBufferCapacity overrides how many messages all lanes together may
// hold, so a test can reach the shared bound without queueing the whole of it.
func withLaneBufferCapacity(capacity int) peerLanesOptionFunc {
	return func(l *peerLanes) {
		l.bufferCapacity = capacity
	}
}

// withLaneByteCapacity overrides how much payload all lanes together may hold,
// so a test can reach the shared bound without queueing the whole of it.
func withLaneByteCapacity(capacity int) peerLanesOptionFunc {
	return func(l *peerLanes) {
		l.byteCapacity = capacity
	}
}

func newPeerLanes(opts ...peerLanesOptionFunc) *peerLanes {
	lanes := &peerLanes{
		clock:          clockwork.NewRealClock(),
		logger:         log.NewNopLogger(),
		metrics:        NopMetrics(),
		bufferCapacity: msgQueueSize,
		byteCapacity:   laneByteCapacity,
		lanes:          make(map[types.NodeID]*peerLane),
		sessions:       make(map[types.NodeID]uint64),
		ready:          make(chan struct{}, 1),
		settled:        make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(lanes)
	}
	lanes.lastGC = lanes.clock.Now()
	return lanes
}

// admit registers a peer's connection as live and returns the session that
// stands for it. The session must accompany the peer's messages for them to be
// admitted, so a message from a session that has since been purged cannot create
// or revive a lane.
//
// A peer that already has a live session — a repeated up for a connection that
// never went down, such as a validator-set update — keeps it, so its in-flight
// messages are not stranded by a fresh session.
func (l *peerLanes) admit(peerID types.NodeID) uint64 {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	if session, ok := l.sessions[peerID]; ok {
		return session
	}
	l.nextSession++
	l.sessions[peerID] = l.nextSession
	return l.nextSession
}

// send queues a message in its sender's lane, creating the lane if the peer has
// none.
//
// A lane at capacity gives up its OLDEST message, never the arriving one.
// Votes and commits are only valid for the height and round they were made in,
// so refusing the newest would spend the peer's turns on messages that are
// guaranteed stale while every fresh one is discarded — muting the peer for
// rounds on end with no attacker involved. For a block part neither choice is
// better: dropping either one leaves the block unassembled and waiting on the
// round to time out and start again, so the policy that is right for the
// time-valid majority applies to all of them.
//
// Neither shedding nor a canceled context is an error. The peer that fills its
// lane is the one sending as fast as this node can accept, which is what an
// honest peer at full stretch does, and a canceled context means this node is
// shutting down; since the caller turns any error into a peer error, reporting
// either would feed the eviction machinery with exactly the peers worth keeping.
//
// A message with no sender reaches here only when a test routes it here. It
// gets a lane of its own under the empty node ID and takes its turn like any
// other, but is never charged: this node's own work is not what the budget
// bounds.
func (l *peerLanes) send(ctx context.Context, mi msgInfo) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	if !l.enqueue(ctx, mi) {
		return nil
	}

	select {
	case l.ready <- struct{}{}:
	default:
	}
	return nil
}

// enqueue puts the message at the back of its sender's lane, making room for it
// first if that lane, or the node as a whole, is full.
//
// The bounds are independent and the shared ones are smaller than a lane's own
// as soon as more than a few lanes are busy. A lane's own capacity is therefore
// what one peer may hold while the node has room, not a reservation it can
// always claim.
//
// Room is always taken from whoever is using the most of the bound that is
// short — messages for the message count, payload for the byte ceiling — so a
// peer holding little never pays for another peer's backlog.
//
// It reports whether the message was queued: a message whose connection session
// has ended is dropped without creating or reviving a lane, and reports false so
// the caller does not wake a scheduler that has nothing new to serve.
func (l *peerLanes) enqueue(ctx context.Context, mi msgInfo) bool {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	if !l.sessionLive(ctx, mi.PeerID) {
		return false
	}
	l.reclaimIdleLanes()
	lane, ok := l.lanes[mi.PeerID]
	if !ok {
		lane = &peerLane{}
		l.lanes[mi.PeerID] = lane
	}
	lane.lastActive = l.clock.Now()
	size := laneMessageBytes(mi)
	switch {
	case len(lane.queue) >= laneCapacity:
		// The arriving peer's own lane is full. Dropping its oldest keeps its
		// freshest messages, which is what the time-valid majority needs, and the
		// credit that lane saved for the dropped head goes with it.
		l.shedOldest(lane, "lane full")
	case l.queued >= l.bufferCapacity:
		// Room for the arriving message is taken from whoever holds the most,
		// which is another peer's lane. Drop that lane's newest, not its head:
		// its head and the credit saved for it stay paired, so a peer cannot have
		// a colluder evict its expensive head and then spend the saved credit on
		// a burst of cheap messages served out of turn.
		l.shedNewest(l.longestLane(), "all lanes full")
	}
	// Shed until the arriving message fits: one message can be worth hundreds
	// of the ones already queued, so a single eviction need not free enough.
	// Stopping when there is nothing left to shed is what keeps this from
	// spinning if the accounting and the rotation ever disagree; a message
	// larger than the whole ceiling is then admitted alone rather than refused,
	// which is the direction that keeps the node making progress.
	for l.queuedBytes+size > l.byteCapacity {
		if !l.shedNewest(l.heaviestLane(), "lanes hold too much") {
			break
		}
	}
	// Read after shedding: making room may have emptied this very lane, which
	// takes it out of the rotation and means it has to rejoin.
	wasActive := lane.active()
	lane.queue = append(lane.queue, queuedMsg{msgInfo: mi, bytes: size})
	lane.bytes += size
	l.queued++
	l.queuedBytes += size
	if !wasActive {
		// A lane joins the rotation at the tail, so activity cannot buy a peer an
		// earlier turn than the peers already waiting.
		lane.deficit = 0
		lane.turnOpen = false
		l.rotation = append(l.rotation, mi.PeerID)
	}
	l.sampleLaneDepth()
	return true
}

// sessionLive reports whether a message may be admitted into a lane. This node's
// own messages (no sender) always may. A peer message carrying a session is
// admitted only while that session is the peer's live one, so a message left in
// flight by a connection that has since ended cannot create or revive a lane. A
// peer message carrying no session — a path that predates sessions — keeps the
// former behavior and is admitted.
//
// The caller must hold mtx.
func (l *peerLanes) sessionLive(ctx context.Context, peerID types.NodeID) bool {
	if peerID == "" {
		return true
	}
	session, ok := peerLaneSessionFromCtx(ctx)
	if !ok {
		return true
	}
	live, admitted := l.sessions[peerID]
	return admitted && live == session
}

// recv returns the next message to dispatch, blocking until the rotation has one
// the verification budget can cover. It reports false once ctx is done, which is
// what ends the reader goroutine at shutdown.
func (l *peerLanes) recv(ctx context.Context) (msgInfo, bool) {
	for {
		if !l.awaitSettled(ctx) {
			return msgInfo{}, false
		}
		mi, ok := l.next()
		if !ok {
			select {
			case <-l.ready:
			case <-ctx.Done():
				return msgInfo{}, false
			}
			continue
		}
		if !l.affordable(ctx, mi) {
			if ctx.Err() != nil {
				return msgInfo{}, false
			}
			continue
		}
		// Discard a settlement left behind by a message this scheduler did not
		// hand over, so the next wait can only observe this message's own.
		select {
		case <-l.settled:
		default:
		}
		l.outstanding = mi.PeerID != ""
		return mi, true
	}
}

// settle reports that the consensus goroutine has finished with the message it
// was given, so the scheduler may look at the verification budget again. It
// never blocks: it is called from the consensus goroutine.
func (l *peerLanes) settle() {
	select {
	case l.settled <- struct{}{}:
	default:
	}
}

// awaitSettled blocks until the message already dispatched has been processed.
// It reports false when ctx is done.
//
// Handing over one peer message at a time is what makes the affordability check
// sound: the charges for a message are made while it is verified, so a scheduler
// that read the budget before then would be reading it with those charges still
// to come, and two messages would spend the same tokens.
//
// It needs no deadline of its own: the handoff to the consensus goroutine is
// synchronous, so while that goroutine is busy there is nothing the scheduler
// could do with an earlier release anyway.
func (l *peerLanes) awaitSettled(ctx context.Context) bool {
	if !l.outstanding {
		return true
	}
	select {
	case <-l.settled:
		l.outstanding = false
		return true
	case <-ctx.Done():
		return false
	}
}

// affordable makes room in the verification budget for the whole work the
// message can force, and reports whether it may be dispatched.
//
// Waiting here rather than on the consensus goroutine is what keeps a saturated
// budget from delaying the timeout ticker and this node's own messages: while
// the scheduler waits, the consensus goroutine is free to serve them.
//
// The wait holds up the other lanes, and that is the rate gate doing its job.
// Passing over a message this node cannot afford right now and serving a cheaper
// one instead would be worse than it looks: refusing a message costs nothing, so
// a flood of cheap messages holds the budget below the cost of an expensive one
// indefinitely and that message is never admitted, however honest its sender.
// Waiting is what keeps a granted turn from being thrown away.
//
// Every negative outcome is a silent local drop. Neither an unpriceable message
// nor a budget this node cannot refill in time says anything about the sender,
// and the drop happens before dispatch, so it costs no write-ahead log record.
func (l *peerLanes) affordable(ctx context.Context, mi msgInfo) bool {
	if l.waiter == nil || mi.PeerID == "" {
		return true
	}
	cost, err := budgetedMessageCost(mi.Msg)
	if err != nil {
		l.metrics.VerificationBudgetDrops.Add(1)
		l.logger.Debug("dropping unpriceable peer message", "peer", mi.PeerID, "error", err)
		return false
	}
	admitted := l.waiter.waitFor(ctx, cost)
	// Sample how full the budget is at this check, whether or not the message was
	// admitted: a message dropped for want of budget is exactly when the gauge
	// should read near empty.
	if l.budgetSaturation != nil {
		l.metrics.VerificationBudgetSaturation.Set(l.budgetSaturation.saturation())
	}
	if !admitted {
		l.metrics.VerificationBudgetDrops.Add(1)
		l.logger.Debug("dropping peer message the verification budget cannot cover",
			"peer", mi.PeerID, "cost", cost)
		return false
	}
	return true
}

// next takes the message the rotation owes a turn to, or reports false when
// every lane is empty.
//
// Each turn grants the front lane one quantum and serves as much of its head as
// the lane's deficit now covers. A lane whose head costs more than it has been
// granted keeps its credit and goes to the back, so every other waiting lane is
// served before it is revisited: it is served after the turns it takes to
// accumulate the head's cost and not later, whatever lanes join or leave in the
// meantime.
func (l *peerLanes) next() (msgInfo, bool) {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	l.reclaimIdleLanes()
	for len(l.rotation) > 0 {
		peerID := l.rotation[0]
		lane := l.lanes[peerID]
		if !lane.turnOpen {
			lane.deficit += laneQuantum
			lane.turnOpen = true
		}
		cost := laneTurnCost(lane.queue[0].msgInfo)
		if lane.deficit < cost {
			// The front lane cannot afford its head this turn: it keeps its
			// credit and goes to the back, behind every lane still waiting.
			lane.turnOpen = false
			l.rotateFront()
			continue
		}
		lane.deficit -= cost
		mi := l.popOldest(lane)
		lane.lastActive = l.clock.Now()
		if !lane.active() {
			// A lane that runs dry leaves the rotation and forfeits its credit,
			// so an idle peer cannot save up turns to spend in a burst.
			lane.deficit = 0
			lane.turnOpen = false
			l.removeFromRotation(0)
		}
		l.sampleLaneDepth()
		return mi, true
	}
	return msgInfo{}, false
}

// purgePeer drops everything a peer left behind and retires its lane and its
// connection session. It is called when the peer disconnects: its messages can
// no longer matter, its lane must not keep taking turns from the peers still
// connected, and ending the session is what stops a message the disconnecting
// peer left in flight from recreating the lane once it is gone.
//
// It is idempotent: a peer with no lane and no session — already purged, or
// never admitted — is a no-op.
func (l *peerLanes) purgePeer(peerID types.NodeID) {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	delete(l.sessions, peerID)
	lane, ok := l.lanes[peerID]
	if !ok {
		return
	}
	if lane.active() {
		l.queued -= len(lane.queue)
		l.queuedBytes -= lane.bytes
		l.metrics.PeerLaneDrops.Add(float64(len(lane.queue)))
		l.removeFromRotation(indexOfPeer(l.rotation, peerID))
	}
	delete(l.lanes, peerID)
}

// buffered reports how many messages all lanes hold together.
func (l *peerLanes) buffered() int {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return l.queued
}

// bufferedBytes reports how much payload all lanes hold together.
func (l *peerLanes) bufferedBytes() int {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return l.queuedBytes
}

// sampleLaneDepth records how many lanes are in the rotation and how deep the
// deepest of them is. It is called as lanes are filled and served, which is
// where those two numbers change, and scans only the lanes currently in the
// rotation — bounded by the connection slots the node accepts.
//
// The caller must hold mtx.
func (l *peerLanes) sampleLaneDepth() {
	maxDepth := 0
	for _, peerID := range l.rotation {
		if depth := len(l.lanes[peerID].queue); depth > maxDepth {
			maxDepth = depth
		}
	}
	l.metrics.PeerLaneActiveCount.Set(float64(len(l.rotation)))
	l.metrics.PeerLaneMaxDepth.Set(float64(maxDepth))
}

// popOldest removes the message at the head of the lane. The slot is cleared so
// a served block part is not kept alive by the lane's backing array.
//
// The caller must hold mtx.
func (l *peerLanes) popOldest(lane *peerLane) msgInfo {
	entry := lane.queue[0]
	lane.queue[0] = queuedMsg{}
	lane.queue = lane.queue[1:]
	if len(lane.queue) == 0 {
		lane.queue = nil
	}
	l.accountPopped(lane, entry)
	return entry.msgInfo
}

// popNewest removes the message at the tail of the lane. The slot is cleared so
// a dropped block part is not kept alive by the lane's backing array.
//
// The caller must hold mtx.
func (l *peerLanes) popNewest(lane *peerLane) msgInfo {
	last := len(lane.queue) - 1
	entry := lane.queue[last]
	lane.queue[last] = queuedMsg{}
	lane.queue = lane.queue[:last]
	if len(lane.queue) == 0 {
		lane.queue = nil
	}
	l.accountPopped(lane, entry)
	return entry.msgInfo
}

// accountPopped subtracts a removed message from the lane's and the node's byte
// and message tallies, debiting exactly the charge stored when it was enqueued.
//
// The caller must hold mtx.
func (l *peerLanes) accountPopped(lane *peerLane, entry queuedMsg) {
	lane.bytes -= entry.bytes
	l.queued--
	l.queuedBytes -= entry.bytes
}

// shedOldest makes room in a lane by dropping the message that has waited
// longest, which is also the one most likely to be stale. It reports whether
// anything was dropped.
//
// It is used only to make room in the arriving peer's own lane, so the credit
// the lane saved for the dropped head must go with it. The credit is granted for
// the message at the head; without debiting it the peer could send one expensive
// message, let the lane save up while the head waits, then fill its own lane
// until the head is shed and spend the saved credit on a burst of cheap messages
// served out of turn.
//
// The caller must hold mtx.
func (l *peerLanes) shedOldest(lane *peerLane, reason string) bool {
	if lane == nil || !lane.active() {
		return false
	}
	mi := l.popOldest(lane)
	lane.deficit -= laneTurnCost(mi)
	if lane.deficit < 0 {
		lane.deficit = 0
	}
	l.metrics.PeerLaneDrops.Add(1)
	l.logger.Debug("shedding oldest queued consensus message", "peer", mi.PeerID, "reason", reason)
	if !lane.active() {
		l.removeFromRotation(indexOfPeer(l.rotation, mi.PeerID))
	}
	return true
}

// shedNewest makes room in a lane by dropping the message that arrived most
// recently. It reports whether anything was dropped.
//
// It is used to take room from another peer's lane to fit an arriving message,
// where dropping the head would be wrong twice over. Drop-oldest is a freshness
// choice within one lane, but across lanes there is no shared staleness order —
// one peer's newer message is not a reason to delete another's admitted current
// message. And the credit a lane saves is granted for its head: shedding the
// head cross-lane would strand that credit on a message the node never served,
// which a colluding peer could arrange to fund a cheap burst. Dropping the tail
// keeps the head and its credit paired, and never resets the credit — so an
// expensive head that is the reason a lane is the longest is throttled, not
// silenced.
//
// The caller must hold mtx.
func (l *peerLanes) shedNewest(lane *peerLane, reason string) bool {
	if lane == nil || !lane.active() {
		return false
	}
	mi := l.popNewest(lane)
	l.metrics.PeerLaneDrops.Add(1)
	l.logger.Debug("shedding newest queued consensus message", "peer", mi.PeerID, "reason", reason)
	if !lane.active() {
		l.removeFromRotation(indexOfPeer(l.rotation, mi.PeerID))
	}
	return true
}

// longestLane returns the lane holding the most messages.
//
// The caller must hold mtx.
func (l *peerLanes) longestLane() *peerLane {
	var longest *peerLane
	for _, peerID := range l.rotation {
		lane := l.lanes[peerID]
		if longest == nil || len(lane.queue) > len(longest.queue) {
			longest = lane
		}
	}
	return longest
}

// heaviestLane returns the lane holding the most payload.
//
// The caller must hold mtx.
func (l *peerLanes) heaviestLane() *peerLane {
	var heaviest *peerLane
	for _, peerID := range l.rotation {
		lane := l.lanes[peerID]
		if heaviest == nil || lane.bytes > heaviest.bytes {
			heaviest = lane
		}
	}
	return heaviest
}

// removeFromRotation drops the lane at index i from the service order. The lanes
// behind it keep their order and each moves one place towards the front.
//
// The caller must hold mtx.
func (l *peerLanes) removeFromRotation(i int) {
	if i < 0 || i >= len(l.rotation) {
		return
	}
	l.rotation = append(l.rotation[:i], l.rotation[i+1:]...)
}

// rotateFront moves the front lane to the back of the service order, keeping the
// order of every other lane. It is how a lane that cannot afford its head this
// turn yields to the lanes waiting behind it without losing its place relative
// to them.
//
// The caller must hold mtx.
func (l *peerLanes) rotateFront() {
	if len(l.rotation) <= 1 {
		return
	}
	front := l.rotation[0]
	copy(l.rotation, l.rotation[1:])
	l.rotation[len(l.rotation)-1] = front
}

// reclaimIdleLanes drops the lanes of peers that have sent nothing for
// laneIdleTimeout. Without it every node identity that ever sent a message would
// leave state behind, and identities are free to mint.
//
// The caller must hold mtx.
func (l *peerLanes) reclaimIdleLanes() {
	now := l.clock.Now()
	if now.Sub(l.lastGC) < laneIdleTimeout {
		return
	}
	l.lastGC = now
	for peerID, lane := range l.lanes {
		if !lane.active() && now.Sub(lane.lastActive) >= laneIdleTimeout {
			delete(l.lanes, peerID)
		}
	}
}

// laneMessageBytes is what holding a message costs against the shared byte
// ceiling: a flat overhead for the per-message bookkeeping the lanes carry, the
// message's own marshaled size, and a per-element surcharge for the Go object
// overhead its repeated fields retain beyond their wire bytes.
//
// The marshaled size is used deliberately in place of a hand-written enumeration
// of the fields worth measuring. Such an enumeration is only ever as complete as
// the last person to update it: a vote or commit may carry up to the maximum
// number of vote extensions, and an extension's payload, signature and
// sign_request_id are each unbounded and attacker-controlled; a commit carries a
// quorum hash; a block part carries a Merkle proof of sibling hashes. Any one of
// those, forgotten, lets a near-megabyte message be charged for its overhead
// alone and one lane pin hundreds of megabytes under a 64 MiB ceiling. proto.Size
// counts every field automatically. It walks the message but does not marshal it,
// so it costs no allocation of the payload — an acceptable price next to the
// signature verification each message already draws.
//
// proto.Size measures the wire form, but the lane holds the deserialized Go
// objects, which retain more: every repeated element — each vote extension, each
// Merkle-proof aunt — is a separate allocation with a struct, slice headers and a
// container pointer the wire never carries. Left uncharged, a message declaring
// many near-empty extensions sits under the byte ceiling in the accounting while
// pinning several times its wire size in heap. retainedElementOverhead adds a
// bounded per-element surcharge so the charge is an upper bound on the memory the
// message actually retains, not merely on its wire size.
//
// The marshaled size is scaled up by roundedPayloadBytes, because proto.Size
// counts a slice by its length while the allocator serves it from a size class
// that rounds the length up: the charge must bound the rounded heap, not the
// length. A message that cannot be converted to its wire form is charged the
// overhead alone. Messages reach a lane only after MsgFromProto has accepted them
// at ingress, so the reverse conversion does not fail in practice; charging the
// floor rather than the ceiling on the theoretical failure keeps a malformed
// message from being used to evict the whole backlog.
func laneMessageBytes(mi msgInfo) int {
	return laneMessageOverhead + roundedPayloadBytes(marshaledMessageSize(mi.Msg)) + retainedElementOverhead(mi.Msg)
}

// roundedPayloadBytes scales a marshaled payload size up to a strict upper bound
// on the heap the Go allocator rounds it to. proto.Size counts a byte slice by
// its length, but the runtime serves it from a size class: an allocation past
// 32 KiB is rounded up to a whole number of 8 KiB pages, so a 32,769-byte slice
// pins 40,960 bytes — 25% more than its length. The size classes past that
// threshold are spaced by no more than that 25%, so multiplying the marshaled
// size by 13/10 is a strict upper bound on the rounding of the large contiguous
// payloads a flood uses to pin memory. The rounding of a message's own small
// structs and of many near-empty repeated elements is bounded separately, by
// laneMessageOverhead and the fixed per-element surcharges, whose sizes already
// leave room for it.
func roundedPayloadBytes(marshaled int) int {
	return marshaled * laneAllocatorSlackNum / laneAllocatorSlackDen
}

// retainedElementOverhead is the Go object overhead a message's repeated elements
// retain beyond their wire bytes — the fixed per-element cost proto.Size does not
// see, since it measures the wire form and not the deserialized objects the lane
// holds. Only the schedulable types carry such fields: a vote or commit its vote
// extensions, a block part its Merkle-proof aunts. Types with none, and the nil
// message a test may route, are charged nothing extra.
func retainedElementOverhead(msg Message) int {
	switch m := msg.(type) {
	case *VoteMessage:
		if m.Vote == nil {
			return 0
		}
		return laneVoteExtensionOverhead * len(m.Vote.VoteExtensions)
	case *CommitMessage:
		if m.Commit == nil {
			return 0
		}
		return laneVoteExtensionOverhead * len(m.Commit.ThresholdVoteExtensions)
	case *BlockPartMessage:
		if m.Part == nil {
			return 0
		}
		return laneProofAuntOverhead * len(m.Part.Proof.Aunts)
	}
	return 0
}

// marshaledMessageSize is the size of a message on the wire, or zero if it cannot
// be converted to its proto form.
func marshaledMessageSize(msg Message) int {
	pb, err := MsgToProto(msg)
	if err != nil || pb == nil {
		return 0
	}
	return proto.Size(pb)
}

// laneTurnCost is what serving a message costs its lane's deficit: the
// verification work it can force, and never less than the turn on the consensus
// goroutine that even a message verifying nothing takes.
func laneTurnCost(mi msgInfo) int {
	cost, err := budgetedMessageCost(mi.Msg)
	if err != nil || cost < baseMessageCost {
		return baseMessageCost
	}
	return cost
}

func indexOfPeer(rotation []types.NodeID, peerID types.NodeID) int {
	for i, id := range rotation {
		if id == peerID {
			return i
		}
	}
	return -1
}
