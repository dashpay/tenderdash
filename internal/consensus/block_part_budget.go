package consensus

import (
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/dashpay/tenderdash/types"
)

const (
	// blockPartProofRateBytes is how many bytes of block-part payload one peer
	// may have hashed on its behalf, per second, for proofs that turn out not to
	// check out.
	//
	// It is what gives the node-wide bound its number: across every connection
	// slot the wasted hashing stays a low single-digit percentage of one core,
	// which is the coarse ceiling this exists to put on the vector. A peer whose
	// parts do verify never draws on it at all.
	blockPartProofRateBytes = 4 * float64(types.BlockPartSizeBytes)

	// blockPartProofBurstBytes is how much of that a peer may spend at once.
	//
	// A peer gossiping parts against a stale view of our part set produces
	// proofs that legitimately fail, so the burst has to absorb a run of them
	// without the peer noticing; thirty-two full-size parts is several seconds
	// of one peer's part gossip.
	blockPartProofBurstBytes = 32 * int(types.BlockPartSizeBytes)

	// blockPartProofIdleTimeout is how long a peer's spent allowance is
	// remembered once it stops arriving. Node identities are free to mint, so
	// what an attacker leaves behind by cycling them has to be reclaimed.
	blockPartProofIdleTimeout = 60 * time.Second
)

// blockPartProofBudget bounds the hashing a peer can force by sending block
// parts whose merkle proof does not verify.
//
// The part-set caps bound accepted parts. They do not bound rejected ones: a
// failed proof leaves the slot empty, so the next copy aimed at the same index
// is hashed from scratch, and verifying a proof hashes the whole ~64 kB leaf
// before it can find the mismatch. Nothing charged for that.
//
// It is deliberately per-peer rather than node-wide. A node-wide bucket would
// let one peer's garbage stop this node from assembling a block at all, which
// trades a bounded CPU cost for an unbounded liveness one; per peer, the
// node-wide bound follows from the connection ceiling instead, and only the
// peer producing the bad proofs is affected.
//
// A part that verifies gives its sender the cost of that part back. That is
// what keeps a peer whose proofs failed for a while — because its view of our
// part set was behind — from staying throttled once it catches up: its first
// accepted part pays for the next, and its valid parts flow at full rate again
// after one refill interval. Crediting one part rather than restoring the whole
// allowance is what stops the credit being farmed: a peer that interleaves
// valid parts with bad ones gains exactly one part of hashing per valid part it
// lands, not a fresh burst.
type blockPartProofBudget struct {
	clock clockwork.Clock

	mtx    sync.Mutex
	peers  map[types.NodeID]*peerProofAllowance
	lastGC time.Time
}

// peerProofAllowance is one peer's token bucket, denominated in bytes of leaf
// hashing. It is kept by hand rather than with rate.Limiter because the credit
// a verified part earns has no equivalent there: a reservation cannot be handed
// back once its time has come.
type peerProofAllowance struct {
	tokens     float64
	lastRefill time.Time
	lastActive time.Time
}

// blockPartProofBudgetOptionFunc overrides a default parameter of a
// blockPartProofBudget.
type blockPartProofBudgetOptionFunc func(*blockPartProofBudget)

// withBlockPartProofClock sets the time source the allowances are metered
// against. The default is the wall clock; a test injects a fake clock to
// advance time explicitly.
func withBlockPartProofClock(clock clockwork.Clock) blockPartProofBudgetOptionFunc {
	return func(b *blockPartProofBudget) {
		b.clock = clock
	}
}

func newBlockPartProofBudget(opts ...blockPartProofBudgetOptionFunc) *blockPartProofBudget {
	budget := &blockPartProofBudget{
		clock: clockwork.NewRealClock(),
		peers: make(map[types.NodeID]*peerProofAllowance),
	}
	for _, opt := range opts {
		opt(budget)
	}
	budget.lastGC = budget.clock.Now()
	return budget
}

// allow reports whether a part of the given size may be verified on the
// sender's behalf. It takes nothing: only a proof that actually fails is
// charged, so a peer sending parts that verify is never held up.
//
// A part with no sender is this node's own, and one replayed from the
// write-ahead log is this node's own history; neither is what this bounds, and
// charging a replay would be worse than pointless — the log is consumed far
// faster than the allowance refills, so a run of failures recorded during
// normal operation would throttle the replay of the valid parts that followed
// it and the node would fail to rebuild a block it had already assembled.
func (b *blockPartProofBudget) allow(peerID types.NodeID, fromReplay bool, size int) bool {
	if b == nil || peerID == "" || fromReplay {
		return true
	}
	b.mtx.Lock()
	defer b.mtx.Unlock()

	now := b.clock.Now()
	b.reclaimIdle(now)
	allowance, ok := b.peers[peerID]
	if !ok {
		return true
	}
	b.refill(allowance, now)
	return allowance.tokens >= float64(size)
}

// chargeFailure records the hashing spent on a proof that did not check out.
func (b *blockPartProofBudget) chargeFailure(peerID types.NodeID, fromReplay bool, size int) {
	if b == nil || peerID == "" || fromReplay {
		return
	}
	b.mtx.Lock()
	defer b.mtx.Unlock()

	allowance := b.allowanceFor(peerID, b.clock.Now())
	allowance.tokens -= float64(size)
	if allowance.tokens < 0 {
		allowance.tokens = 0
	}
}

// accepted credits a peer for a part that verified, so the work it just made
// this node do for a real part pays for the next one.
func (b *blockPartProofBudget) accepted(peerID types.NodeID, fromReplay bool, size int) {
	if b == nil || peerID == "" || fromReplay {
		return
	}
	b.mtx.Lock()
	defer b.mtx.Unlock()

	allowance, ok := b.peers[peerID]
	if !ok {
		// Nothing spent, nothing to credit.
		return
	}
	b.refill(allowance, b.clock.Now())
	allowance.tokens += float64(size)
	if allowance.tokens >= float64(blockPartProofBurstBytes) {
		// Back to a clean slate: keeping the entry would only cost memory.
		delete(b.peers, peerID)
	}
}

// allowanceFor returns the peer's bucket, starting it full if it has none.
//
// The caller must hold mtx.
func (b *blockPartProofBudget) allowanceFor(peerID types.NodeID, now time.Time) *peerProofAllowance {
	allowance, ok := b.peers[peerID]
	if !ok {
		allowance = &peerProofAllowance{tokens: float64(blockPartProofBurstBytes), lastRefill: now}
		b.peers[peerID] = allowance
	} else {
		b.refill(allowance, now)
	}
	allowance.lastActive = now
	return allowance
}

// refill adds the tokens that have accrued since the bucket was last touched.
//
// The caller must hold mtx.
func (b *blockPartProofBudget) refill(allowance *peerProofAllowance, now time.Time) {
	elapsed := now.Sub(allowance.lastRefill)
	if elapsed > 0 {
		allowance.tokens += elapsed.Seconds() * blockPartProofRateBytes
		if allowance.tokens > float64(blockPartProofBurstBytes) {
			allowance.tokens = float64(blockPartProofBurstBytes)
		}
	}
	allowance.lastRefill = now
	allowance.lastActive = now
}

// reclaimIdle forgets peers that have sent nothing for a while.
//
// The caller must hold mtx.
func (b *blockPartProofBudget) reclaimIdle(now time.Time) {
	if now.Sub(b.lastGC) < blockPartProofIdleTimeout {
		return
	}
	b.lastGC = now
	for peerID, allowance := range b.peers {
		if now.Sub(allowance.lastActive) >= blockPartProofIdleTimeout {
			delete(b.peers, peerID)
		}
	}
}
