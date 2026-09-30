package consensus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"

	"github.com/dashpay/tenderdash/types"
)

// commitKey identifies a commit by what the application's extension check
// depends on: height, round, block and extension vector. Commits with equal keys
// differ at most in their threshold block signature.
type commitKey struct {
	height     int64
	round      int32
	blockID    string
	extensions [sha256.Size]byte
}

func newCommitKey(commit *types.Commit) commitKey {
	var buf []byte
	for _, ext := range commit.ThresholdVoteExtensions {
		bz, err := ext.Marshal()
		if err != nil {
			panic(fmt.Errorf("marshal commit vote extension: %w", err))
		}
		buf = binary.AppendUvarint(buf, uint64(len(bz)))
		buf = append(buf, bz...)
	}
	return commitKey{
		height:     commit.Height,
		round:      commit.Round,
		blockID:    commit.BlockID.Key(),
		extensions: sha256.Sum256(buf),
	}
}

// commitCandidate is a peer's authenticated commit kept until the block it
// commits arrives.
type commitCandidate struct {
	commit     *types.Commit
	key        commitKey
	peerID     types.NodeID
	fromReplay bool
}

// commitCandidates keeps the commits TryAddCommit receives while another commit
// for the height is parked awaiting its block. The application can check a
// commit's extensions only once that block is processed, so the parked commit
// may still be rejected; a peer that sent us a commit never sends it again at
// this height and round, so these are the only replacements those peers offer.
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
}

// add keeps commit as peerID's candidate for height.
func (c *commitCandidates) add(height int64, commit *types.Commit, peerID types.NodeID, fromReplay bool) {
	c.resetUnless(height)
	candidate := commitCandidate{commit: commit, key: newCommitKey(commit), peerID: peerID, fromReplay: fromReplay}
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

// holds reports whether peerID's slot keeps the commit identified by key for
// height. Another peer's equal commit does not count: its sender may still
// replace it or disconnect, which would take it out of the queue.
func (c *commitCandidates) holds(height int64, peerID types.NodeID, key commitKey) bool {
	return c.height == height && slices.ContainsFunc(c.candidates, func(candidate commitCandidate) bool {
		return candidate.peerID == peerID && candidate.key == key
	})
}

// retainable reports whether candidate may keep its slot.
func (c *commitCandidates) retainable(candidate commitCandidate) bool {
	return candidate.fromReplay || candidate.peerID == "" || c.connected == nil || c.connected(candidate.peerID)
}

// take removes and returns the candidates for height, oldest first.
func (c *commitCandidates) take(height int64) []commitCandidate {
	c.resetUnless(height)
	taken := c.candidates
	c.candidates = nil
	return taken
}

func (c *commitCandidates) resetUnless(height int64) {
	if c.height != height {
		c.height = height
		c.candidates = nil
	}
}
