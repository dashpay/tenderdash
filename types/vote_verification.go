package types

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
)

// ErrVoteVerificationMismatch reports a VoteVerification offered for a vote, a
// chain or a validator it was not produced for, or for a vote whose signed
// content has changed since. It is a wiring fault rather than the sender's: a
// verification is minted inside this process and the vote it covers was
// authentic when it was minted, so a mismatch means this process paired the
// evidence with the wrong vote or wrote over the right one — not that the vote
// arrived forged.
var ErrVoteVerificationMismatch = errors.New("vote verification does not match the vote being added")

// VoteVerification is evidence that one specific vote's signatures were
// verified, and against exactly which parameters. A vote set accepts it in
// place of verifying the same vote again, but only after checking that every
// recorded parameter is one it would have verified with itself.
//
// Whether a vote's signatures check out is a function of the vote's contents,
// the chain, the quorum and the signer's key alone. A verification naming the
// same values therefore establishes exactly what the vote set's own check would
// have established. Recording those values — rather than carrying a bare
// "already verified" flag — is what makes evidence produced for one chain,
// quorum, validator or vote worthless anywhere else.
//
// The contents are named by the digests that were signed rather than by the
// vote's address in memory. A vote is shared with the application and stored in
// the set afterwards, so an identity that says which object was verified says
// nothing about which bytes it held at the time. For the same reason everything
// recorded here is either an immutable value or a copy this package owns:
// anything held by reference would report whatever its owner last wrote,
// including a value rewritten between the check and the vote's admission.
//
// Only VerifyVoteSignatures can produce a populated VoteVerification, and only
// after the signatures verified. The zero value names no vote and is refused by
// every vote set, so code that has verified nothing cannot construct something
// a vote set accepts.
type VoteVerification struct {
	// vote is the vote the evidence was minted for. It identifies the intended
	// subject, and mismatching it is a wiring fault worth its own message, but
	// it establishes nothing about content: a vote reachable through this same
	// pointer can be rewritten after the signatures were checked. signHashes
	// and signatures are what bind the evidence to bytes.
	vote       *Vote
	chainID    string
	quorumType btcjson.LLMQType
	quorumHash crypto.QuorumHash
	proTxHash  ProTxHash

	// pubKeyType and pubKeyBytes name the signer key the signatures were checked
	// against, as material rather than as the key object itself. A key is an
	// interface over bytes its supplier owns: the bytes are shared with every
	// copy of the validator and can be rewritten after the check, and the
	// implementation behind the interface is whatever the caller passed — one
	// that answers yes to every question is as easy to supply as a real one.
	// Naming the type that ran the check and a copy of the bytes it ran on lets
	// the key a vote set already trusts decide the comparison, instead of
	// delegating that decision to the key the evidence was minted with.
	pubKeyType  reflect.Type
	pubKeyBytes []byte

	// signHashes and signatures are exactly what was handed to the signature
	// check: the digest of the block followed by one digest per
	// threshold-recoverable vote extension, and the signature verified against
	// each. Both are copies, so nothing that later writes through the vote can
	// change what the evidence says was verified.
	signHashes [][]byte
	signatures [][]byte
}

// VerifyVoteSignatures verifies a vote's block signature and, only if that
// succeeds, its vote-extension signatures, charging each stage to budget when
// one is given. On success it returns evidence a vote set accepts in place of
// repeating the same work.
//
// It performs the same check VoteSet.AddVote performs, so a caller that needs
// the result of that check before the vote is added — to decide whether to ask
// the application about the vote's extensions, say — can run it here and leave
// the vote set with nothing left to verify.
func VerifyVoteSignatures(
	vote *Vote,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	pubKey crypto.PubKey,
	proTxHash ProTxHash,
	budget VerificationBudget,
) (VoteVerification, error) {
	if vote == nil {
		return VoteVerification{}, ErrVoteNil
	}
	signData, signs, err := vote.verifyReportingSigns(chainID, quorumType, quorumHash, pubKey, proTxHash, budget)
	if err != nil {
		return VoteVerification{}, err
	}
	signHashes, signatures := signedContent(signData, signs)
	return VoteVerification{
		vote:        vote,
		chainID:     chainID,
		quorumType:  quorumType,
		quorumHash:  crypto.QuorumHash(bytes.Clone(quorumHash)),
		proTxHash:   ProTxHash(bytes.Clone(proTxHash)),
		pubKeyType:  reflect.TypeOf(pubKey),
		pubKeyBytes: bytes.Clone(pubKey.Bytes()),
		signHashes:  signHashes,
		signatures:  signatures,
	}, nil
}

// signedContent copies what a signature check consumed: the digests it verified
// against, and the signatures it verified. Two votes yielding the same pair are
// indistinguishable to that check, so a verification of one is a verification of
// the other; any difference means the check that was run does not describe the
// vote being offered.
//
// The copies matter. The signatures are the vote's own slices, and the vote is
// reachable — and mutable — from outside this package for as long as the
// evidence is in flight.
func signedContent(signData QuorumSignData, signs QuorumSigns) (signHashes, signatures [][]byte) {
	signHashes = make([][]byte, 0, 1+len(signData.VoteExtensionSignItems))
	signHashes = append(signHashes, bytes.Clone(signData.Block.SignHash))
	for _, item := range signData.VoteExtensionSignItems {
		signHashes = append(signHashes, bytes.Clone(item.SignHash))
	}

	signatures = make([][]byte, 0, 1+len(signs.VoteExtensionSignatures))
	signatures = append(signatures, bytes.Clone(signs.BlockSign))
	for _, sig := range signs.VoteExtensionSignatures {
		signatures = append(signatures, bytes.Clone(sig))
	}

	return signHashes, signatures
}

// checkMatches reports whether v is evidence about this exact vote under these
// exact parameters. It compares what a signature check would compare, so a vote
// it does not reject establishes what verifying that vote again would
// establish — no more, and nothing that check would have caught less.
//
// The vote's content is compared by rebuilding the digests a check would run on
// it now and holding them against the digests that were checked, together with
// the signatures. That costs a marshal and a hash per signature — never a
// pairing — so it establishes what a second verification would establish at a
// small fraction of the price the single-verification budget was set for.
func (v VoteVerification) checkMatches(
	vote *Vote,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	pubKey crypto.PubKey,
	proTxHash ProTxHash,
) error {
	switch {
	case v.vote == nil:
		return fmt.Errorf("%w: nothing was verified", ErrVoteVerificationMismatch)
	case v.vote != vote:
		return fmt.Errorf("%w: a different vote was verified", ErrVoteVerificationMismatch)
	case v.chainID != chainID:
		return fmt.Errorf("%w: verified for chain %q, added to %q",
			ErrVoteVerificationMismatch, v.chainID, chainID)
	case v.quorumType != quorumType:
		return fmt.Errorf("%w: verified for quorum type %d, added under %d",
			ErrVoteVerificationMismatch, v.quorumType, quorumType)
	case !bytes.Equal(v.quorumHash, quorumHash):
		return fmt.Errorf("%w: verified for quorum %X, added under %X",
			ErrVoteVerificationMismatch, v.quorumHash, quorumHash)
	case pubKey == nil || reflect.TypeOf(pubKey) != v.pubKeyType || !bytes.Equal(v.pubKeyBytes, pubKey.Bytes()):
		return fmt.Errorf("%w: verified against a different validator public key",
			ErrVoteVerificationMismatch)
	case !bytes.Equal(v.proTxHash, proTxHash):
		return fmt.Errorf("%w: verified for validator %X, added as %X",
			ErrVoteVerificationMismatch, v.proTxHash, proTxHash)
	}

	// Everything a fresh verification establishes about the vote's shape before
	// it touches a signature — the validator it names, the key's size, and that
	// only a precommit carries vote extensions. None of it is covered by a
	// digest, so a vote can be rewritten into a shape no verification would
	// accept while every recorded digest still matches. It is the same function
	// the verification itself ran, called rather than restated, so the two
	// cannot come to disagree.
	if err := vote.verifyBasic(proTxHash, pubKey); err != nil {
		return fmt.Errorf("%w: %s", ErrVoteVerificationMismatch, err)
	}

	signData, err := makeVerifyQuorumSigns(chainID, quorumType, quorumHash, vote.ToProto())
	if err != nil {
		return fmt.Errorf("%w: the vote no longer yields signing data: %s",
			ErrVoteVerificationMismatch, err)
	}
	signHashes, signatures := signedContent(signData, vote.makeQuorumSigns())
	if !equalByteSlices(v.signHashes, signHashes) {
		return fmt.Errorf("%w: the vote's signed content is not what was verified",
			ErrVoteVerificationMismatch)
	}
	if !equalByteSlices(v.signatures, signatures) {
		return fmt.Errorf("%w: the vote no longer carries the signatures that were verified",
			ErrVoteVerificationMismatch)
	}

	return nil
}

func equalByteSlices(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}
