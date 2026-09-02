package consensus

import (
	sync "github.com/sasha-s/go-deadlock"
)

// catchupTracker holds back block proposals after block sync handed the node to
// consensus while it was still behind the network.
//
// Nothing else tells consensus that the node is behind. A long-lived validator
// handed a historical height is very likely the genuine proposer of the next
// height as well, and the block it would build carries present-day application
// state, which collides with the real block for that height as soon as the
// network's commit arrives - a collision the node cannot restart its way out of
// (dashpay/tenderdash#1413). Voting and following consensus are safe and stay
// enabled; only proposing waits.
type catchupTracker struct {
	mtx sync.Mutex
	// peerHeight reports the highest height any connected peer claims, and is
	// nil whenever the node may propose
	peerHeight func() int64
	// committed records a block committed through consensus since the handover
	committed bool
}

// arm holds back proposals until the node has committed a block through
// consensus and no connected peer reports a height above its own. peerHeight
// reports the highest height any connected peer claims, 0 when none does.
//
// Callers arm this only for a handover that left the node provably behind, so a
// node no peer claims to be ahead of - a solo validator, a fresh network - never
// enters the window at all.
func (t *catchupTracker) arm(peerHeight func() int64) {
	if t == nil {
		return
	}
	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.peerHeight = peerHeight
	t.committed = false
}

// blockCommitted records a block committed through consensus.
func (t *catchupTracker) blockCommitted() {
	if t == nil {
		return
	}
	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.committed = true
}

// mayPropose reports whether the node may build a proposal for height.
//
// Only positive evidence ends the window: a peer reporting a height no higher
// than ours, or a block committed through consensus while no peer reports one.
// A peer that has reported no height yet says nothing either way, and every
// peer is in that state for the first moments after the handover - which is
// exactly when the node is at its most behind.
//
// The highest height claimed by any peer decides, not the median or a quorum of
// them: the peer set behind the original failure was five seed nodes, and letting
// peers that hold no blocks outvote the one reporting the tip is what let the
// node propose in the first place. So one peer claiming an impossible height can
// keep a validator from proposing - a lost proposer, not lost safety - and only
// while the node is inside a window it entered by being behind. The window then
// closes for good: nothing but another block-sync handover reopens it.
func (t *catchupTracker) mayPropose(height int64) bool {
	if t == nil {
		return true
	}
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if t.peerHeight == nil {
		return true
	}
	peerHeight := t.peerHeight()
	if peerHeight > height || (peerHeight == 0 && !t.committed) {
		return false
	}
	t.peerHeight = nil
	return true
}
