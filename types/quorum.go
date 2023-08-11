package types

import (
	"fmt"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
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
	commit.ThresholdVoteExtensions = c.ExtensionSigns
}

// QuorumSigns holds all created signatures, block, state and for each recovered vote-extensions
type QuorumSigns struct {
	BlockSign      []byte
	ExtensionSigns []ThresholdExtensionSign
}

// NewQuorumSignsFromCommit creates and returns QuorumSigns using threshold signatures from a commit
func NewQuorumSignsFromCommit(commit *Commit) QuorumSigns {
	return QuorumSigns{
		BlockSign:      commit.ThresholdBlockSignature,
		ExtensionSigns: commit.ThresholdVoteExtensions,
	}
}

// ThresholdExtensionSign is used for keeping extension and recovered threshold signature
type ThresholdExtensionSign struct {
	Extension          []byte
	ThresholdSignature []byte
}

// MakeThresholdExtensionSigns creates and returns the list of ThresholdExtensionSign for given VoteExtensions container
func MakeThresholdExtensionSigns(voteExtensions VoteExtensions) []ThresholdExtensionSign {
	if voteExtensions == nil {
		return nil
	}
	extensions := voteExtensions[tmproto.VoteExtensionType_THRESHOLD_RECOVER]
	if len(extensions) == 0 {
		return nil
	}
	thresholdSigns := make([]ThresholdExtensionSign, len(extensions))
	for i, ext := range extensions {
		thresholdSigns[i] = ThresholdExtensionSign{
			Extension:          ext.Extension,
			ThresholdSignature: ext.Signature,
		}
	}
	return thresholdSigns
}

// ThresholdExtensionSignFromProto transforms a list of protobuf ThresholdVoteExtension
// into the list of domain ThresholdExtensionSign
func ThresholdExtensionSignFromProto(protoExtensions []*tmproto.VoteExtension) []ThresholdExtensionSign {
	if len(protoExtensions) == 0 {
		return nil
	}
	extensions := make([]ThresholdExtensionSign, len(protoExtensions))
	for i, ext := range protoExtensions {
		extensions[i] = ThresholdExtensionSign{
			Extension:          ext.Extension,
			ThresholdSignature: ext.Signature,
		}
	}
	return extensions
}

// ThresholdExtensionSignToProto transforms a list of domain ThresholdExtensionSign
// into the list of protobuf VoteExtension
func ThresholdExtensionSignToProto(extensions []ThresholdExtensionSign) []*tmproto.VoteExtension {
	if len(extensions) == 0 {
		return nil
	}
	protoExtensions := make([]*tmproto.VoteExtension, len(extensions))
	for i, ext := range extensions {
		protoExtensions[i] = &tmproto.VoteExtension{
			Extension: ext.Extension,
			Signature: ext.ThresholdSignature,
		}
	}
	return protoExtensions
}

// MakeThresholdVoteExtensions creates a list of ThresholdExtensionSign from the list of VoteExtension
// and recovered threshold signatures. The lengths of vote-extensions and threshold signatures must be the same
func MakeThresholdVoteExtensions(extensions []VoteExtension, thresholdSigs [][]byte) []ThresholdExtensionSign {
	thresholdExtensions := make([]ThresholdExtensionSign, len(extensions))
	for i, ext := range extensions {
		thresholdExtensions[i] = ThresholdExtensionSign{
			Extension:          ext.Extension,
			ThresholdSignature: thresholdSigs[i],
		}
	}
	return thresholdExtensions
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

func (q *QuorumSingsVerifier) verifyVoteExtensions(
	pubKey crypto.PubKey,
	signs QuorumSigns,
) error {
	if !q.shouldVerifyVoteExtensions {
		return nil
	}
	sings := signs.ExtensionSigns
	signItems := q.Extensions[tmproto.VoteExtensionType_THRESHOLD_RECOVER]
	if len(signItems) == 0 {
		return nil
	}
	if len(signItems) != len(sings) {
		return fmt.Errorf("count of threshold vote extension signatures (%d) doesn't match with recoverable vote extensions (%d)",
			len(sings), len(signItems),
		)
	}
	for i, ext := range sings {
		if !pubKey.VerifySignatureDigest(signItems[i].ID, ext.ThresholdSignature) {
			return fmt.Errorf("threshold vote-extension signature is invalid (%d) %X",
				i, signItems[i].Raw)
		}
	}
	return nil
}
