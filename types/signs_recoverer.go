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
// the state fills with signatures from the votes
func NewSignsRecoverer(votes []*Vote, opts ...func(*SignsRecoverer)) *SignsRecoverer {
	sigs := SignsRecoverer{
		quorumReached: true,
	}
	for _, opt := range opts {
		opt(&sigs)
	}
	sigs.init(votes)
	return &sigs
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

func (v *SignsRecoverer) init(votes []*Vote) {
	v.blockSigs = nil
	v.stateSigs = nil
	v.validatorProTxHashes = nil

	for _, vote := range votes {
		v.addVoteSigs(vote)
	}
}

func (v *SignsRecoverer) addVoteSigs(vote *Vote) {
	if vote == nil {
		return
	}

	v.blockSigs = append(v.blockSigs, vote.BlockSignature)
	v.validatorProTxHashes = append(v.validatorProTxHashes, vote.ValidatorProTxHash)
	v.addVoteExtensionSigs(vote)
}

// Add threshold-recovered vote extensions
func (v *SignsRecoverer) addVoteExtensionSigs(vote *Vote) {
	if len(vote.VoteExtensions) == 0 {
		return
	}

	// initialize vote extensions
	if v.voteExtensions.IsEmpty() {
		v.voteExtensions = vote.VoteExtensions.Copy()
	}

	// sanity check; this should be detected on higher layers
	if vote.Type != types.PrecommitType || vote.BlockID.IsNil() {
		panic(fmt.Sprintf("only non-nil precommits can have vote extensions, got: %s", vote.String()))
	}

	if len(vote.VoteExtensions) != len(v.voteExtensions) {
		panic(fmt.Sprintf("received vote extensions with different length: current %d, received %d",
			len(v.voteExtensions), len(vote.VoteExtensions)))
	}

	// append signatures from this vote to each extension
	for i, ext := range vote.VoteExtensions {
		if recoverable, ok := (v.voteExtensions[i]).(ThresholdVoteExtensionIf); ok {
			if err := recoverable.AddThresholdSignature(vote.ValidatorProTxHash, ext.GetSignature()); err != nil {
				panic(fmt.Errorf("failed to add vote %s to recover vote extension threshold sig: %w", vote.String(), err))
			}
			v.voteExtensions[i] = recoverable
		}
	}
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
