package types

import (
	"fmt"

	"github.com/dashpay/tenderdash/crypto/bls12381"
)

// SignsRecoverer is used to recover threshold block, state, and vote-extension signatures
// it's possible to avoid recovering state and vote-extension for specific case
type SignsRecoverer struct {
	blockSigs            [][]byte
	stateSigs            [][]byte
	validatorProTxHashes [][]byte
	// List of all threshold-recovered vote extensions, indexed by vote extension number
	voteThresholdExts VoteExtensions
	// voteThresholdExtensionSigs is a list of signatures for each threshold-recovered vote-extension, indexed by vote extension number
	voteThresholdExtensionSigs [][][]byte

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

func (v *SignsRecoverer) init(votes []*Vote) {
	v.blockSigs = nil
	v.stateSigs = nil
	v.validatorProTxHashes = nil
	v.voteThresholdExtensionSigs = nil
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
	v.addVoteExtensionSigs(vote.VoteExtensions)
}

// Add threshold-recovered vote extensions
func (v *SignsRecoverer) addVoteExtensionSigs(voteExtensions VoteExtensions) {
	// Skip non-threshold vote-extensions
	thresholdExtensions := voteExtensions.Filter(func(ext VoteExtensionIf) bool {
		return ext.IsThresholdRecoverable()
	})
	// initialize vote extensions if it's empty
	if len(v.voteThresholdExts) == 0 && !thresholdExtensions.IsEmpty() {
		v.voteThresholdExts = thresholdExtensions.Copy()
		v.voteThresholdExtensionSigs = make([][][]byte, len(thresholdExtensions))
	}

	// sanity check; this should be detected on higher layers
	if len(v.voteThresholdExts) != len(thresholdExtensions) {
		panic(fmt.Sprintf("received vote extensions with different length: current %d, new %d", len(v.voteThresholdExts), len(thresholdExtensions)))
	}

	// append signatures from this vote to each extension
	for i, ext := range thresholdExtensions {
		v.voteThresholdExtensionSigs[i] = append(v.voteThresholdExtensionSigs[i], ext.GetSignature())
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

	// initialize threshold vote extensions with empty signatures
	quorumSigs.ThresholdVoteExtensions = v.voteThresholdExts.Filter(func(ext VoteExtensionIf) bool { return ext.IsThresholdRecoverable() }).Copy()
	for i := range quorumSigs.ThresholdVoteExtensions {
		quorumSigs.ThresholdVoteExtensions[i].SetSignature(nil)
	}

	// for each vote extension, if it's threshold-recoverable, recover its signature
	thresholdExtIndex := 0
	for extIndex, extension := range quorumSigs.ThresholdVoteExtensions {
		if extension.IsThresholdRecoverable() {
			extensionSignatures := v.voteThresholdExtensionSigs[thresholdExtIndex]
			thresholdExtIndex++

			if len(extensionSignatures) > 0 {
				thresholdSignature, err := bls12381.RecoverThresholdSignatureFromShares(extensionSignatures, v.validatorProTxHashes)
				if err != nil {
					return fmt.Errorf("error recovering vote-extension %d threshold signature: %w", extIndex, err)
				}
				quorumSigs.ThresholdVoteExtensions[extIndex].SetSignature(thresholdSignature)
			} else {
				return fmt.Errorf("vote extension %d does not have any signatures for threshold-recovering", extIndex)
			}
		}
	}

	return nil
}
