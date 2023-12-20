package types

import (
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
func (c *CommitSigns) CopyToCommit(commit *Commit) {
	commit.QuorumHash = c.QuorumHash
	commit.ThresholdBlockSignature = c.BlockSign
	commit.ThresholdVoteExtensions = c.ThresholdVoteExtensions
}

// QuorumSigns holds all created signatures, block, state and for each recovered vote-extensions
type QuorumSigns struct {
	BlockSign []byte
	// Signed vote extensions
	ThresholdVoteExtensions VoteExtensions
}

// NewQuorumSignsFromCommit creates and returns QuorumSigns using threshold signatures from a commit.
//
// Note it only uses threshold-revoverable vote extension signatures, as non-threshold signatures are not included in the commit
func NewQuorumSignsFromCommit(commit *Commit) QuorumSigns {
	return QuorumSigns{
		BlockSign:               commit.ThresholdBlockSignature,
		ThresholdVoteExtensions: commit.ThresholdVoteExtensions,
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
	if !pubKey.VerifySignatureDigest(q.Block.ID, signs.BlockSign) {
		return fmt.Errorf(
			"threshold block signature is invalid: (%X) signID=%X: %w",
			q.Block.Raw,
			q.Block.ID,
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

	thresholdSigs := signs.ThresholdVoteExtensions.GetSignatures()
	signItems := q.ThresholdVoteExtensions
	if len(signItems) == 0 {
		return nil
	}
	if len(signItems) != len(thresholdSigs) {
		return fmt.Errorf("count of threshold vote extension signatures (%d) doesn't match with recoverable vote extensions (%d)",
			len(thresholdSigs), len(signItems),
		)
	}

	for i, sig := range thresholdSigs {
		if !pubKey.VerifySignatureDigest(signItems[i].ID, sig) {
			return fmt.Errorf("vote-extension %d signature is invalid: %X", i,
				signItems[i].Raw)
		}
	}
	return nil
}
