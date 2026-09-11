package types

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
)

// errCommitProofMismatch reports a commit proof offered for a commit, a chain or
// a quorum it was not produced for, or for a commit whose signed content has
// changed since — or no proof at all. ValidatorSet.VerifyCommitUnlessVerified
// answers it by verifying the commit in full, so it never reaches a caller.
var errCommitProofMismatch = errors.New("commit proof does not match the commit being verified")

// VerifiedCommit is a commit together with, when it has one, proof that the
// commit's threshold signatures were verified and against exactly which
// parameters. ValidatorSet.VerifyCommitUnlessVerified accepts the proof in place
// of verifying the same commit again, but only after checking that every
// parameter it records is one it would have verified with itself.
//
// Only VerifyCommitSignatures attaches a proof, and only after the signatures
// verified. NewUnverifiedCommit carries none, and neither does the zero value,
// which is NewUnverifiedCommit(nil); code that has verified nothing can pass
// either freely, since it only ever leads to a full verification.
//
// The commit is the caller's pointer: it names what the proof is about and is
// never itself evidence of anything. See Commit.
//
// A VerifiedCommit is meaningful only within the process and flow that built it;
// it is never persisted or sent anywhere.
type VerifiedCommit struct {
	commit *Commit
	proof  *commitProof
}

// commitProof is evidence that one specific commit's threshold signatures were
// verified, and against exactly which parameters.
//
// Whether a commit's signatures check out is a function of the commit's signed
// content, the chain, the quorum and the threshold key alone. Evidence naming
// the same values therefore establishes exactly what a fresh verification
// would. Recording those values — rather than carrying a bare "already
// verified" flag — is what makes evidence produced for one chain, quorum,
// height or commit worthless anywhere else.
//
// Everything recorded is an immutable value or a copy this package owns, never
// a reference into the validator set, block ID or commit it was minted from. A
// commit verified against one height's validator set is checked one height on
// against another object holding the same quorum, and anything held by
// reference would report whatever its owner last wrote.
type commitProof struct {
	chainID    string
	height     int64
	blockID    BlockID
	quorumType btcjson.LLMQType
	quorumHash crypto.QuorumHash

	// thresholdKeyType and thresholdKeyBytes name the key the signatures were
	// checked against, as material rather than as the key object itself. A key
	// is an interface over bytes its supplier owns, and the implementation
	// behind it is whatever the caller passed — one that answers yes to every
	// question is as easy to supply as a real one. Recording the type and a copy
	// of the bytes lets the key the consumer trusts decide the comparison.
	thresholdKeyType  reflect.Type
	thresholdKeyBytes []byte

	// signHashes and signatures are exactly what was handed to the signature
	// check: the digest of the block followed by one digest per
	// threshold-recoverable vote extension, and the signature verified against
	// each. A real verification always records at least the block digest.
	signHashes [][]byte
	signatures [][]byte
}

// NewUnverifiedCommit returns commit as a VerifiedCommit without proof, for a
// caller that holds commit but has not verified it. It is only ever verified in
// full.
func NewUnverifiedCommit(commit *Commit) VerifiedCommit {
	return VerifiedCommit{commit: commit}
}

// Commit returns the commit v was built for, as the pointer v was built with.
//
// It is not evidence. Holding a VerifiedCommit proves nothing about the commit
// it returns, which the caller that built v may have changed since, and a proof
// only ever covers the commit ValidatorSet.VerifyCommitUnlessVerified is handed.
// Never verify or validate this commit in place of the one actually received —
// a block's LastCommit is read from the block — or a commit other than the one
// the chain carries could be accepted.
func (v VerifiedCommit) Commit() *Commit {
	return v.commit
}

// VerifyCommitSignatures verifies commit against vals as the commit for
// blockID at height, charging each stage to budget when one is given. It runs
// the same check, and reports the same errors, as ValidatorSet.VerifyCommit;
// on success it returns commit with the proof
// ValidatorSet.VerifyCommitUnlessVerified accepts in place of repeating that
// check. On failure it returns the zero VerifiedCommit.
func VerifyCommitSignatures(
	vals *ValidatorSet,
	chainID string,
	blockID BlockID,
	height int64,
	commit *Commit,
	budget VerificationBudget,
) (VerifiedCommit, error) {
	if vals == nil {
		return VerifiedCommit{}, ErrValidatorSetNilOrEmpty
	}
	if commit == nil {
		return VerifiedCommit{}, errors.New("nil commit")
	}
	signData, signs, err := vals.verifyCommitReportingSigns(chainID, blockID, height, commit, budget)
	if err != nil {
		return VerifiedCommit{}, err
	}
	signHashes, signatures := signedContent(signData, signs)
	return VerifiedCommit{
		commit: commit,
		proof: &commitProof{
			chainID:           chainID,
			height:            height,
			blockID:           blockID.Copy(),
			quorumType:        vals.QuorumType,
			quorumHash:        bytes.Clone(vals.QuorumHash),
			thresholdKeyType:  reflect.TypeOf(vals.ThresholdPublicKey),
			thresholdKeyBytes: bytes.Clone(vals.ThresholdPublicKey.Bytes()),
			signHashes:        signHashes,
			signatures:        signatures,
		},
	}, nil
}

// checkMatches reports whether p is evidence about this exact commit under
// these exact parameters. A nil p — a VerifiedCommit without proof — matches
// nothing. It compares what a verification would compare, so a commit it does
// not reject establishes what verifying that commit again would establish — no
// more, and nothing that verification would have caught less.
//
// The commit's content is compared by running the checks a verification runs
// before it touches a signature, rebuilding the digests it would verify, and
// holding them and the commit's signatures against what was verified. That
// costs a marshal and a hash per signature — never a pairing.
//
// commit is compared by content, never by identity with the commit the proof
// was minted with: a pointer says nothing about what it points to now.
func (p *commitProof) checkMatches(
	chainID string,
	vals *ValidatorSet,
	blockID BlockID,
	height int64,
	commit *Commit,
) error {
	switch {
	case p == nil || len(p.signHashes) == 0:
		return fmt.Errorf("%w: nothing was verified", errCommitProofMismatch)
	case vals == nil:
		return fmt.Errorf("%w: no validator set to verify against", errCommitProofMismatch)
	case commit == nil:
		return fmt.Errorf("%w: no commit to verify", errCommitProofMismatch)
	case p.chainID != chainID:
		return fmt.Errorf("%w: verified for chain %q, offered for %q",
			errCommitProofMismatch, p.chainID, chainID)
	case p.height != height:
		return fmt.Errorf("%w: verified for height %d, offered for %d",
			errCommitProofMismatch, p.height, height)
	case !p.blockID.Equals(blockID):
		return fmt.Errorf("%w: verified for block %v, offered for %v",
			errCommitProofMismatch, p.blockID, blockID)
	case p.quorumType != vals.QuorumType:
		return fmt.Errorf("%w: verified for quorum type %d, offered under %d",
			errCommitProofMismatch, p.quorumType, vals.QuorumType)
	case !bytes.Equal(p.quorumHash, vals.QuorumHash):
		return fmt.Errorf("%w: verified for quorum %X, offered under %X",
			errCommitProofMismatch, p.quorumHash, vals.QuorumHash)
	case vals.ThresholdPublicKey == nil ||
		reflect.TypeOf(vals.ThresholdPublicKey) != p.thresholdKeyType ||
		!bytes.Equal(p.thresholdKeyBytes, vals.ThresholdPublicKey.Bytes()):
		return fmt.Errorf("%w: verified against a different threshold public key",
			errCommitProofMismatch)
	}

	signData, err := vals.commitSignData(chainID, blockID, height, commit)
	if err != nil {
		return fmt.Errorf("%w: the commit no longer passes verification's preliminary checks: %s",
			errCommitProofMismatch, err)
	}
	signHashes, signatures := signedContent(signData, NewQuorumSignsFromCommit(commit))
	if !equalByteSlices(p.signHashes, signHashes) {
		return fmt.Errorf("%w: the commit's signed content is not what was verified",
			errCommitProofMismatch)
	}
	if !equalByteSlices(p.signatures, signatures) {
		return fmt.Errorf("%w: the commit no longer carries the signatures that were verified",
			errCommitProofMismatch)
	}

	return nil
}
