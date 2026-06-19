package types

import (
	"fmt"

	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

// SignsRecoverer is used to recover threshold block, state, and vote-extension signatures
// it's possible to avoid recovering state and vote-extension for specific case
type SignsRecoverer struct {
	blockSigs            [][]byte
	stateSigs            [][]byte
	validatorProTxHashes [][]byte
	// List of all vote extensions. Order matters.
	voteExtensions VoteExtensions

	// canonicalVoteExtCount, when set, is the only vote-extension count that
	// contributes to vote-extension recovery; votes carrying any other count are
	// skipped (their block signature is still used). It is determined by the
	// caller from aggregate voting power (see VoteSet.canonicalVoteExtensionCount)
	// so a Byzantine minority offering a different count can neither halt nor
	// corrupt recovery (SEC-001).
	canonicalVoteExtCount    int
	hasCanonicalVoteExtCount bool

	// true when the recovery of vote extensions was already executed
	voteExtensionsRecovered bool

	quorumReached bool
}

// WithQuorumReached sets a flag at SignsRecoverer to recovers threshold signatures for stateID and vote-extensions
func WithQuorumReached(quorumReached bool) func(*SignsRecoverer) {
	return func(r *SignsRecoverer) {
		r.quorumReached = quorumReached
	}
}

// WithCanonicalVoteExtensionCount restricts vote-extension recovery to the votes
// carrying exactly count extensions. Every other vote (fewer OR more, including
// zero) is excluded from vote-extension recovery while still contributing its
// block signature. The caller is responsible for choosing count as the value
// backed by at least the recovery threshold voting power.
func WithCanonicalVoteExtensionCount(count int) func(*SignsRecoverer) {
	return func(r *SignsRecoverer) {
		r.canonicalVoteExtCount = count
		r.hasCanonicalVoteExtCount = true
	}
}

// NewSignsRecoverer creates and returns a new instance of SignsRecoverer
// the state fills with signatures from the votes.
//
// When a canonical vote-extension count is supplied (WithCanonicalVoteExtensionCount),
// only votes carrying that count contribute to vote-extension recovery and no
// count mismatch is ever treated as an error. Without it (legacy callers passing
// already-consistent votes), a precommit with no extensions is skipped and a
// non-zero count mismatch is reported as an error.
func NewSignsRecoverer(votes []*Vote, opts ...func(*SignsRecoverer)) (*SignsRecoverer, error) {
	sigs := SignsRecoverer{
		quorumReached: true,
	}
	for _, opt := range opts {
		opt(&sigs)
	}
	if err := sigs.init(votes); err != nil {
		return nil, err
	}
	return &sigs, nil
}

// Recover recovers threshold signatures for block, state and vote-extensions
func (v *SignsRecoverer) Recover() (*QuorumSigns, error) {
	thresholdSigns := &QuorumSigns{}
	recoverFuncs := []func(signs *QuorumSigns) error{
		v.recoverBlockSig,
		v.recoverVoteExtensionSigs,
	}
	for _, fn := range recoverFuncs {
		err := fn(thresholdSigns)
		if err != nil {
			return nil, err
		}
	}
	return thresholdSigns, nil
}

// Helper function that returns deep copy of recovered vote extensions with signatures from QuorumSigns.
//
// Note that this method doesn't recover threshold signatures.
// It requires to call Recover() method first.
//
// ## Panics
//
// Panics when the count of threshold vote extension signatures in QuorumSigns doesn't match recoverable vote extensions
func (v *SignsRecoverer) GetVoteExtensions(qs QuorumSigns) VoteExtensions {
	if len(qs.VoteExtensionSignatures) != len(v.voteExtensions) {
		panic(fmt.Sprintf("count of threshold vote extension signatures (%d) doesn't match recoverable vote extensions (%d)",
			len(qs.VoteExtensionSignatures), len(v.voteExtensions)))
	}
	exts := v.voteExtensions.Copy()
	for i, ext := range exts {
		ext.SetSignature(qs.VoteExtensionSignatures[i])
	}

	return exts
}

func (v *SignsRecoverer) init(votes []*Vote) error {
	v.blockSigs = nil
	v.stateSigs = nil
	v.validatorProTxHashes = nil
	v.voteExtensions = nil

	for _, vote := range votes {
		if err := v.addVoteSigs(vote); err != nil {
			return err
		}
	}
	return nil
}

func (v *SignsRecoverer) addVoteSigs(vote *Vote) error {
	if vote == nil {
		return nil
	}

	v.blockSigs = append(v.blockSigs, vote.BlockSignature)
	v.validatorProTxHashes = append(v.validatorProTxHashes, vote.ValidatorProTxHash)
	return v.addVoteExtensionSigs(vote)
}

// addVoteExtensionSigs feeds a single vote's vote-extension signature shares
// into the threshold recovery state.
//
// Only votes whose extension count matches the canonical count contribute their
// extension shares; every other vote is skipped here but still contributes its
// block signature (appended by the caller). This is the SEC-001 fix: honest
// validators run the same deterministic ABCI ExtendVote for a given
// (height, round) and so all produce the same extension count, while a Byzantine
// minority offering a different count (zero OR non-zero) is excluded. The
// canonical count is supplied by the caller and is the count backed by at least
// the recovery threshold voting power (VoteSet.canonicalVoteExtensionCount), so
// the excluded set can never be the honest majority. Excluding a vote's
// extension share is safe because every vote's own extension signatures are
// verified against its validator's key before admission (Vote.Verify ->
// QuorumSignData.Verify), so a Byzantine validator can only influence the
// *count* of extensions it offers, never their content.
func (v *SignsRecoverer) addVoteExtensionSigs(vote *Vote) error {
	if v.hasCanonicalVoteExtCount {
		// Recovery path: contribute only if this vote carries the canonical count.
		if len(vote.VoteExtensions) != v.canonicalVoteExtCount {
			return nil
		}
	}

	if len(vote.VoteExtensions) == 0 {
		// Nothing to recover from this vote: either the canonical count is zero
		// (e.g. vote extensions disabled, or a nil-block precommit), or - for
		// legacy callers without a canonical count - the precommit simply carries
		// no extensions. It contributes only its block signature.
		return nil
	}

	// Only non-nil precommits may carry vote extensions.
	if vote.Type != types.PrecommitType || vote.BlockID.IsNil() {
		return fmt.Errorf("only non-nil precommits can have vote extensions, got: %s", vote.String())
	}

	// Establish the canonical extension set from the first contributing vote.
	if v.voteExtensions.IsEmpty() {
		v.voteExtensions = vote.VoteExtensions.Copy()
	}

	// Every contributing vote carries the canonical count, so this is a defensive
	// consistency check. With a canonical count supplied it cannot fire; without
	// one (legacy callers) it reports a genuine non-zero count mismatch.
	if len(vote.VoteExtensions) != len(v.voteExtensions) {
		return fmt.Errorf("received vote extensions with different length: current %d, received %d",
			len(v.voteExtensions), len(vote.VoteExtensions))
	}

	// append signatures from this vote to each extension
	for i, ext := range vote.VoteExtensions {
		if recoverable, ok := (v.voteExtensions[i]).(ThresholdVoteExtensionIf); ok {
			if err := recoverable.AddThresholdSignature(vote.ValidatorProTxHash, ext.GetSignature()); err != nil {
				return fmt.Errorf("failed to add vote %s to recover vote extension threshold sig: %w", vote.String(), err)
			}
			v.voteExtensions[i] = recoverable
		}
	}
	return nil
}

func (v *SignsRecoverer) recoverBlockSig(thresholdSigns *QuorumSigns) error {
	var err error
	thresholdSigns.BlockSign, err = bls12381.RecoverThresholdSignatureFromShares(v.blockSigs, v.validatorProTxHashes)
	if err != nil {
		return fmt.Errorf("error recovering threshold block sig: %w", err)
	}
	return nil
}

// recoverVoteExtensionSigs recovers threshold signatures for vote-extensions
func (v *SignsRecoverer) recoverVoteExtensionSigs(quorumSigs *QuorumSigns) error {
	if !v.quorumReached {
		return nil
	}

	if quorumSigs.VoteExtensionSignatures == nil {
		quorumSigs.VoteExtensionSignatures = make([][]byte, len(v.voteExtensions))
	}

	if len(v.voteExtensions) != len(quorumSigs.VoteExtensionSignatures) {
		return fmt.Errorf("count of threshold vote extension signatures (%d) doesn't match recoverable vote extensions (%d)",
			len(quorumSigs.VoteExtensionSignatures), len(v.voteExtensions))
	}

	for i, ext := range v.voteExtensions {
		if extension, ok := ext.(ThresholdVoteExtensionIf); ok {
			sig, err := extension.ThresholdRecover()
			if err != nil {
				return fmt.Errorf("error recovering threshold signature for vote extension %d: %w", i, err)
			}
			quorumSigs.VoteExtensionSignatures[i] = sig
		}
	}

	v.voteExtensionsRecovered = true

	return nil
}
