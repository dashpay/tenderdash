package consensus

import (
	"github.com/dashpay/tenderdash/types"
)

// maxCommitCandidates caps how many commits commitCandidates holds for one
// height. Each is at most one vote-channel message, so the memory held is
// bounded by maxCommitCandidates times that message size limit.
const maxCommitCandidates = 32

// commitCandidate is a commit received from a peer while another commit for the
// same height was parked, kept unverified until it is needed.
type commitCandidate struct {
	commit     *types.Commit
	peerID     types.NodeID
	fromReplay bool
}

// commitCandidates keeps the commits TryAddCommit would otherwise drop because a
// commit for the height is already parked awaiting its block. A peer that sent
// us a commit marks us as holding one and never sends it again, so once the
// parked commit's extensions are rejected these are the only replacements the
// node will see at this round.
//
// Each peer has one slot, which only that peer's later commits overwrite, so a
// peer cannot displace another's commit by resending; slots keep their arrival
// order, which WAL replay reproduces. Beyond maxCommitCandidates new peers'
// commits are dropped; the round change scheduled after an unresolved
// rejection makes peers gossip their commits again. Entries belong to a single
// height and are discarded as soon as another height is seen.
//
// Only the consensus goroutine touches it, through the actions it runs, so it
// needs no lock.
type commitCandidates struct {
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
	for i := range c.candidates {
		if c.candidates[i].peerID == peerID {
			c.candidates[i] = candidate
			return
		}
	}
	if len(c.candidates) < maxCommitCandidates {
		c.candidates = append(c.candidates, candidate)
	}
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
