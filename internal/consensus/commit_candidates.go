package consensus

import (
	"slices"

	"github.com/dashpay/tenderdash/types"
)

// commitCandidate is a peer's commit kept, unverified, until a rejected commit
// needs replacing.
type commitCandidate struct {
	commit     *types.Commit
	peerID     types.NodeID
	fromReplay bool
}

// commitCandidates keeps the commits TryAddCommit would otherwise drop: those
// received while another commit for the height is parked awaiting its block, and
// those it could not afford to verify. A peer that sent us a commit marks us as
// holding one and never sends it again at this height and round, so after a
// rejection these are the only replacements the node will see from those peers.
//
// Each connected peer has one slot, which only that peer's later commits
// overwrite, so a peer cannot displace another's commit by resending; slots keep
// their arrival order, which WAL replay reproduces. Adding a commit first frees
// the slots of peers that have disconnected, so the slots are bounded by the
// connections p2p accepts; a peer that reconnects sends its commit again.
// Replayed and local entries are exempt: replay runs before any peer connects,
// and the WAL bounds them. Entries belong to a single height and are discarded
// as soon as another height is seen.
//
// Only the consensus goroutine touches it, through the actions it runs, so it
// needs no lock of its own.
type commitCandidates struct {
	// connected reports whether a peer's connection is live; nil treats every
	// peer as connected.
	connected  func(types.NodeID) bool
	height     int64
	candidates []commitCandidate
	// recovering is set while a rejected commit is being replaced, so a
	// replacement rejected in turn leaves the remaining options to the loop
	// already running instead of starting a nested one.
	recovering bool
}

// add keeps commit as peerID's candidate for height.
func (c *commitCandidates) add(height int64, commit *types.Commit, peerID types.NodeID, fromReplay bool) {
	c.resetUnless(height)
	candidate := commitCandidate{commit: commit, peerID: peerID, fromReplay: fromReplay}
	c.candidates = slices.DeleteFunc(c.candidates, func(other commitCandidate) bool {
		return other.peerID != peerID && !c.retainable(other)
	})
	for i := range c.candidates {
		if c.candidates[i].peerID == peerID {
			c.candidates[i] = candidate
			return
		}
	}
	c.candidates = append(c.candidates, candidate)
}

// retainable reports whether candidate may keep its slot.
func (c *commitCandidates) retainable(candidate commitCandidate) bool {
	return candidate.fromReplay || candidate.peerID == "" || c.connected == nil || c.connected(candidate.peerID)
}

// pop removes and returns the oldest candidate for height.
func (c *commitCandidates) pop(height int64) (commitCandidate, bool) {
	c.resetUnless(height)
	if len(c.candidates) == 0 {
		return commitCandidate{}, false
	}
	next := c.candidates[0]
	c.candidates[0] = commitCandidate{}
	c.candidates = c.candidates[1:]
	return next, true
}

func (c *commitCandidates) resetUnless(height int64) {
	if c.height != height {
		c.height = height
		c.candidates = nil
	}
}
