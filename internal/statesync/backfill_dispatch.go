package statesync

import (
	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/types"
)

// backfillDispatch decides which peers a single backfill run may fetch from.
//
// A peer that supplies an unverifiable commit is quarantined: evicted from the
// pool at once and refused both on dispatch and on re-admission, so neither the
// fetch loop nor the router's asynchronous eviction can re-offer it during the
// run. It also tracks how many peers are out of the pool, which is what
// distinguishes a working run from a stalled one.
//
// Quarantine and re-admission are serialized against each other here, so a peer
// quarantined while a fetch was holding it cannot be handed back to the pool by
// that fetch. Peers arriving from peer updates go into the pool directly and are
// gated on their next dispatch instead.
type backfillDispatch struct {
	mtx         sync.Mutex
	peers       *peerList
	quarantined map[types.NodeID]struct{}
	// accounted is the pool's hand-out count as this run has observed it. It
	// trails the pool's own count for as long as a peer is in transit between
	// being handed out and being acquired, which is what keeps such a peer from
	// looking like an empty pool. It starts from the count the pool already
	// carries, which outlives any single backfill run.
	accounted   uint64
	inFlight    int
	stallReason error
}

func newBackfillDispatch(peers *peerList) *backfillDispatch {
	_, taken := peers.Availability()
	return &backfillDispatch{
		peers:       peers,
		quarantined: make(map[types.NodeID]struct{}),
		accounted:   taken,
	}
}

// acquire accounts a peer just taken from the pool as an outstanding fetch and
// reports whether it may be used. A quarantined peer reports false and must be
// dropped by the caller: it is not returned to the pool.
func (d *backfillDispatch) acquire(peer types.NodeID) bool {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	d.accounted++
	if _, bad := d.quarantined[peer]; bad {
		return false
	}
	d.inFlight++
	return true
}

// release ends an outstanding fetch, returning the peer to the pool when reuse is
// true and it was not quarantined while its fetch was in flight.
func (d *backfillDispatch) release(peer types.NodeID, reuse bool) {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	d.inFlight--
	if _, bad := d.quarantined[peer]; bad || !reuse {
		return
	}
	d.peers.Append(peer)
}

// quarantine bars a peer from the rest of the run and records reason as the run's
// failure cause if it is the first one.
func (d *backfillDispatch) quarantine(peer types.NodeID, reason error) {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	d.quarantined[peer] = struct{}{}
	if d.stallReason == nil {
		d.stallReason = reason
	}
	d.peers.Remove(peer)
}

// stalled returns the recorded failure reason once the run can no longer make
// progress - a peer was quarantined, no peer is left in the pool, none is in
// transit out of it and no fetch is outstanding - and nil otherwise.
//
// A peer connecting after this observation is not waited for: a run whose peers
// all misbehaved should surface that reason rather than block on an empty pool
// until the context is canceled. The quarantine requirement is what keeps an
// ordinary lull between fetches from ending a healthy run.
func (d *backfillDispatch) stalled() error {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	available, taken := d.peers.Availability()
	if d.stallReason == nil || d.inFlight > 0 || available > 0 || taken != d.accounted {
		return nil
	}
	return d.stallReason
}
