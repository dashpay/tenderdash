package types

import (
	"bytes"
	"fmt"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/log"
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

// QuorumSingsVerifier ...
type QuorumSingsVerifier struct {
	QuorumSignData
	shouldVerifyBlock          bool
	shouldVerifyVoteExtensions bool
	logger                     log.Logger
}

// WithVerifyExtensions sets a flag that tells QuorumSingsVerifier to verify vote-extension signatures or not
func WithVerifyExtensions(shouldVerify bool) func(*QuorumSingsVerifier) {
	return func(verifier *QuorumSingsVerifier) {
		verifier.shouldVerifyVoteExtensions = shouldVerify
	}
}

// WithVerifyBlock sets a flag that tells QuorumSingsVerifier to verify block signature or not
func WithVerifyBlock(shouldVerify bool) func(*QuorumSingsVerifier) {
	return func(verifier *QuorumSingsVerifier) {
		verifier.shouldVerifyBlock = shouldVerify
	}
}

// WithVerifyReachedQuorum sets a flag that tells QuorumSingsVerifier to verify
// vote-extension and stateID signatures or not
func WithVerifyReachedQuorum(quorumReached bool) func(*QuorumSingsVerifier) {
	return func(verifier *QuorumSingsVerifier) {
		verifier.shouldVerifyVoteExtensions = quorumReached
	}
}

// WithLogger sets a logger
func WithLogger(logger log.Logger) func(*QuorumSingsVerifier) {
	return func(verifier *QuorumSingsVerifier) {
		verifier.logger = logger
	}
}

// NewQuorumSignsVerifier creates and returns an instance of QuorumSingsVerifier that is used for verification
// quorum signatures
func NewQuorumSignsVerifier(quorumData QuorumSignData, opts ...func(*QuorumSingsVerifier)) *QuorumSingsVerifier {
	verifier := &QuorumSingsVerifier{
		QuorumSignData:             quorumData,
		shouldVerifyBlock:          true,
		shouldVerifyVoteExtensions: true,
		logger:                     log.NewNopLogger(),
	}
	for _, opt := range opts {
		opt(verifier)
	}
	return verifier
}

// Verify verifies quorum data using public key and passed signatures
func (q *QuorumSingsVerifier) Verify(pubKey crypto.PubKey, signs QuorumSigns) error {
	err := q.verifyBlock(pubKey, signs)
	if err != nil {
		return err
	}
	return q.verifyVoteExtensions(pubKey, signs)
}

func (q *QuorumSingsVerifier) verifyBlock(pubKey crypto.PubKey, signs QuorumSigns) error {
	if !q.shouldVerifyBlock {
		return nil
	}
	if !pubKey.VerifySignatureDigest(q.Block.SignHash, signs.BlockSign) {
		return fmt.Errorf(
			"threshold block signature is invalid: (%X) signID=%X: %w",
			q.Block.Msg,
			q.Block.SignHash,
			ErrVoteInvalidBlockSignature,
		)
	}
	return nil
}

// verify threshold-recoverable vote extensions signatures
func (q *QuorumSingsVerifier) verifyVoteExtensions(
	pubKey crypto.PubKey,
	signs QuorumSigns,
) error {
	if !q.shouldVerifyVoteExtensions {
		return nil
	}

	thresholdSigs := signs.VoteExtensionSignatures
	signItems := q.VoteExtensionSignItems
	if len(signItems) == 0 {
		return nil
	}
	if len(signItems) != len(thresholdSigs) {
		return fmt.Errorf("count of threshold vote extension signatures (%d) doesn't match recoverable vote extensions (%d)",
			len(thresholdSigs), len(signItems),
		)
	}

	for i, sig := range thresholdSigs {
		if !pubKey.VerifySignatureDigest(signItems[i].SignHash, sig) {
			return fmt.Errorf("vote-extension %d signature is invalid: raw %X, signature %X, pubkey %X, sigHash: %X",
				i,
				signItems[i].Msg,
				sig,
				pubKey.Bytes(),
				signItems[i].SignHash,
			)
		}
	}
	return nil
}
