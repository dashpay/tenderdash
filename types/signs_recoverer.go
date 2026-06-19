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

// NewSignsRecoverer creates and returns a new instance of SignsRecoverer
// the state fills with signatures from the votes.
//
// Precommits that carry no vote extensions contribute only their block
// signature and are skipped for vote-extension recovery (see
// addVoteExtensionSigs). It returns an error only for genuinely malformed
// input, for example a non-precommit vote that carries vote extensions, or a
// precommit whose non-zero extension count differs from the others'.
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
// A precommit that carries no vote extensions contributes its block signature
// (already appended by the caller) but nothing to vote-extension recovery, and
// is otherwise ignored here. This is deliberate and is the SEC-001 liveness
// fix: honest validators run the same deterministic ABCI ExtendVote for a given
// (height, round), so they all produce the same, non-zero number of vote
// extensions. A precommit that arrives with zero extensions for the
// about-to-commit block is therefore either provably Byzantine (it withheld its
// extensions) or comes from a chain with vote extensions disabled, in which
// case *every* vote carries zero and there is nothing to recover. Either way,
// skipping it - instead of returning an error that the caller turns into a
// process-wide panic once the quorum threshold is crossed (types/vote_set.go
// addVerifiedVote) - lets the honest majority's extension signatures still be
// recovered. This restores the v1.5.x behaviour that PR #1342 inadvertently
// dropped. Dropping the share is safe because every vote's own extension
// signatures are verified against its validator's key before admission
// (Vote.Verify -> QuorumSignData.Verify), so a Byzantine validator can only
// influence the *count* of extensions it offers, never their content.
func (v *SignsRecoverer) addVoteExtensionSigs(vote *Vote) error {
	if len(vote.VoteExtensions) == 0 {
		return nil
	}

	// Only non-nil precommits may carry vote extensions.
	if vote.Type != types.PrecommitType || vote.BlockID.IsNil() {
		return fmt.Errorf("only non-nil precommits can have vote extensions, got: %s", vote.String())
	}

	// Establish the canonical extension set from the first vote that actually
	// carries extensions.
	if v.voteExtensions.IsEmpty() {
		v.voteExtensions = vote.VoteExtensions.Copy()
	}

	// Every contributing (non-empty) vote must carry the same number of
	// extensions as the canonical set. A differing *non-zero* count has no safe
	// canonical interpretation (unlike the zero case handled above), so it is
	// reported as an error rather than silently dropped.
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
