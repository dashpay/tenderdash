package types

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
)

// errCommitVerificationMismatch reports a CommitVerification offered for a
// commit, a chain or a quorum it was not produced for, or for a commit whose
// signed content has changed since. ValidatorSet.VerifyCommitUnlessVerified
// answers it by verifying the commit in full, so it never reaches a caller.
var errCommitVerificationMismatch = errors.New("commit verification does not match the commit being verified")

// CommitVerification is evidence that one specific commit's threshold
// signatures were verified, and against exactly which parameters.
// ValidatorSet.VerifyCommitUnlessVerified accepts it in place of verifying the
// same commit again, but only after checking that every recorded parameter is
// one it would have verified with itself.
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
//
// Only VerifyCommitSignatures can produce a populated CommitVerification, and
// only after the signatures verified. The zero value names no commit and covers
// nothing, so code that has verified nothing can pass it freely: it only ever
// leads to a full verification.
//
// The evidence is meaningful only within the process and flow that minted it;
// it is never persisted or sent anywhere.
type CommitVerification struct {
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

// VerifyCommitSignatures verifies commit against vals as the commit for
// blockID at height, charging each stage to budget when one is given. It runs
// the same check, and reports the same errors, as ValidatorSet.VerifyCommit;
// on success it also returns evidence ValidatorSet.VerifyCommitUnlessVerified
// accepts in place of repeating that check.
func VerifyCommitSignatures(
	vals *ValidatorSet,
	chainID string,
	blockID BlockID,
	height int64,
	commit *Commit,
	budget VerificationBudget,
) (CommitVerification, error) {
	if vals == nil {
		return CommitVerification{}, ErrValidatorSetNilOrEmpty
	}
	if commit == nil {
		return CommitVerification{}, errors.New("nil commit")
	}
	signData, signs, err := vals.verifyCommitReportingSigns(chainID, blockID, height, commit, budget)
	if err != nil {
		return CommitVerification{}, err
	}
	signHashes, signatures := signedContent(signData, signs)
	return CommitVerification{
		chainID:           chainID,
		height:            height,
		blockID:           blockID.Copy(),
		quorumType:        vals.QuorumType,
		quorumHash:        bytes.Clone(vals.QuorumHash),
		thresholdKeyType:  reflect.TypeOf(vals.ThresholdPublicKey),
		thresholdKeyBytes: bytes.Clone(vals.ThresholdPublicKey.Bytes()),
		signHashes:        signHashes,
		signatures:        signatures,
	}, nil
}

// checkMatches reports whether v is evidence about this exact commit under
// these exact parameters. It compares what a verification would compare, so a
// commit it does not reject establishes what verifying that commit again would
// establish — no more, and nothing that verification would have caught less.
//
// The commit's content is compared by running the checks a verification runs
// before it touches a signature, rebuilding the digests it would verify, and
// holding them and the commit's signatures against what was verified. That
// costs a marshal and a hash per signature — never a pairing.
func (v CommitVerification) checkMatches(
	chainID string,
	vals *ValidatorSet,
	blockID BlockID,
	height int64,
	commit *Commit,
) error {
	switch {
	case len(v.signHashes) == 0:
		return fmt.Errorf("%w: nothing was verified", errCommitVerificationMismatch)
	case vals == nil:
		return fmt.Errorf("%w: no validator set to verify against", errCommitVerificationMismatch)
	case commit == nil:
		return fmt.Errorf("%w: no commit to verify", errCommitVerificationMismatch)
	case v.chainID != chainID:
		return fmt.Errorf("%w: verified for chain %q, offered for %q",
			errCommitVerificationMismatch, v.chainID, chainID)
	case v.height != height:
		return fmt.Errorf("%w: verified for height %d, offered for %d",
			errCommitVerificationMismatch, v.height, height)
	case !v.blockID.Equals(blockID):
		return fmt.Errorf("%w: verified for block %v, offered for %v",
			errCommitVerificationMismatch, v.blockID, blockID)
	case v.quorumType != vals.QuorumType:
		return fmt.Errorf("%w: verified for quorum type %d, offered under %d",
			errCommitVerificationMismatch, v.quorumType, vals.QuorumType)
	case !bytes.Equal(v.quorumHash, vals.QuorumHash):
		return fmt.Errorf("%w: verified for quorum %X, offered under %X",
			errCommitVerificationMismatch, v.quorumHash, vals.QuorumHash)
	case vals.ThresholdPublicKey == nil ||
		reflect.TypeOf(vals.ThresholdPublicKey) != v.thresholdKeyType ||
		!bytes.Equal(v.thresholdKeyBytes, vals.ThresholdPublicKey.Bytes()):
		return fmt.Errorf("%w: verified against a different threshold public key",
			errCommitVerificationMismatch)
	}

	signData, err := vals.commitSignData(chainID, blockID, height, commit)
	if err != nil {
		return fmt.Errorf("%w: the commit no longer passes verification's preliminary checks: %s",
			errCommitVerificationMismatch, err)
	}
	signHashes, signatures := signedContent(signData, NewQuorumSignsFromCommit(commit))
	if !equalByteSlices(v.signHashes, signHashes) {
		return fmt.Errorf("%w: the commit's signed content is not what was verified",
			errCommitVerificationMismatch)
	}
	if !equalByteSlices(v.signatures, signatures) {
		return fmt.Errorf("%w: the commit no longer carries the signatures that were verified",
			errCommitVerificationMismatch)
	}

	return nil
}
