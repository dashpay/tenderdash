package types

import (
	"bytes"
	"fmt"
)

// CommitSigns is used to combine threshold signatures and quorum-hash that were used
type CommitSigns struct {
	QuorumSigns
	QuorumHash []byte
}

// CopyToCommit copies threshold signature to commit
//
// commit.ThresholdVoteExtensions must be initialized
func (c *CommitSigns) CopyToCommit(commit *Commit) {
	commit.QuorumHash = c.QuorumHash
	commit.ThresholdBlockSignature = c.BlockSign
	if len(c.VoteExtensionSignatures) != len(commit.ThresholdVoteExtensions) {
		panic(fmt.Sprintf("count of threshold vote extension signatures (%d) doesn't match vote extensions in commit (%d)",
			len(commit.ThresholdVoteExtensions), len(c.VoteExtensionSignatures),
		))
	}
	for i, ext := range c.VoteExtensionSignatures {
		commit.ThresholdVoteExtensions[i].Signature = bytes.Clone(ext)
	}
}

// QuorumSigns holds all created signatures, block, state and for each recovered vote-extensions
type QuorumSigns struct {
	BlockSign []byte
	// List of vote extensions signatures. Order matters.
	VoteExtensionSignatures [][]byte
}

// NewQuorumSignsFromCommit creates and returns QuorumSigns using threshold signatures from a commit.
//
// Note it only uses threshold-revoverable vote extension signatures, as non-threshold signatures are not included in the commit
func NewQuorumSignsFromCommit(commit *Commit) QuorumSigns {
	sigs := make([][]byte, 0, len(commit.ThresholdVoteExtensions))
	for _, ext := range commit.ThresholdVoteExtensions {
		sigs = append(sigs, ext.Signature)
	}

	return QuorumSigns{
		BlockSign:               commit.ThresholdBlockSignature,
		VoteExtensionSignatures: sigs,
	}
}
