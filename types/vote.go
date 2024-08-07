package types

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const (
	absentVoteStr string = "Vote{absent}"
	nilVoteStr    string = "nil-Vote"
	// MaxVoteBytes is a maximum vote size (including amino overhead).
	MaxVoteBytesBLS12381 int64 = 241
	MaxVoteBytesEd25519  int64 = 209
)

func MaxVoteBytesForKeyType(keyType crypto.KeyType) int64 {
	switch keyType {
	case crypto.Ed25519:
		return MaxVoteBytesEd25519
	case crypto.BLS12381:
		return MaxVoteBytesBLS12381
	}
	return MaxVoteBytesBLS12381
}

var (
	ErrVoteUnexpectedStep                 = errors.New("unexpected step")
	ErrVoteInvalidValidatorIndex          = errors.New("invalid validator index")
	ErrVoteInvalidValidatorAddress        = errors.New("invalid validator address")
	ErrVoteInvalidSignature               = errors.New("invalid signature")
	ErrVoteInvalidBlockHash               = errors.New("invalid block hash")
	ErrVoteNonDeterministicSignature      = errors.New("non-deterministic signature")
	ErrVoteNil                            = errors.New("nil vote")
	ErrVoteInvalidExtension               = errors.New("invalid vote extension")
	ErrVoteExtensionTypeWrongForRequestID = errors.New("provided vote extension type does not support sign request ID")
	ErrVoteInvalidValidatorProTxHash      = errors.New("invalid validator pro_tx_hash")
	ErrVoteInvalidValidatorPubKeySize     = errors.New("invalid validator public key size")
	ErrVoteMissingValidatorPubKey         = errors.New("missing validator public key")
	ErrVoteInvalidBlockSignature          = errors.New("invalid block signature")
	ErrVoteInvalidStateSignature          = errors.New("invalid state signature")
	ErrVoteStateSignatureShouldBeNil      = errors.New("state signature when voting for nil block")
)

type ErrVoteConflictingVotes struct {
	VoteA *Vote
	VoteB *Vote
}

func (err *ErrVoteConflictingVotes) Error() string {
	return fmt.Sprintf("conflicting votes from validator %X: \nA: %+v\nB: %+v", err.VoteA.ValidatorProTxHash, err.VoteA, err.VoteB)
}

func NewConflictingVoteError(vote1, vote2 *Vote) *ErrVoteConflictingVotes {
	return &ErrVoteConflictingVotes{
		VoteA: vote1,
		VoteB: vote2,
	}
}

// Address is hex bytes.
type Address = crypto.Address

type ProTxHash = crypto.ProTxHash

// Vote represents a prevote, precommit, or commit vote from validators for
// consensus.
type Vote struct {
	Type               tmproto.SignedMsgType `json:"type"`
	Height             int64                 `json:"height,string"`
	Round              int32                 `json:"round"`    // assume there will not be greater than 2^32 rounds
	BlockID            BlockID               `json:"block_id"` // zero if vote is nil.
	ValidatorProTxHash ProTxHash             `json:"validator_pro_tx_hash"`
	ValidatorIndex     int32                 `json:"validator_index"`
	BlockSignature     tmbytes.HexBytes      `json:"block_signature"`
	VoteExtensions     VoteExtensions        `json:"vote_extensions"`
}

// VoteFromProto attempts to convert the given serialization (Protobuf) type to
// our Vote domain type. No validation is performed on the resulting vote -
// this is left up to the caller to decide whether to call ValidateBasic or
// ValidateWithExtension.
func VoteFromProto(pv *tmproto.Vote) (*Vote, error) {
	blockID, err := BlockIDFromProto(&pv.BlockID)
	if err != nil {
		return nil, err
	}
	return &Vote{
		Type:               pv.Type,
		Height:             pv.Height,
		Round:              pv.Round,
		BlockID:            *blockID,
		ValidatorProTxHash: pv.ValidatorProTxHash,
		ValidatorIndex:     pv.ValidatorIndex,
		BlockSignature:     pv.BlockSignature,
		VoteExtensions:     VoteExtensionsFromProto(pv.VoteExtensions...),
	}, nil
}

// VoteBlockSignID returns signID that should be signed for the block
func VoteBlockSignID(chainID string, vote *tmproto.Vote, quorumType btcjson.LLMQType, quorumHash []byte) []byte {
	signID := MakeBlockSignItem(chainID, vote, quorumType, quorumHash)
	return signID.SignHash
}

// Copy creates a deep copy of the vote
func (vote *Vote) Copy() *Vote {
	if vote == nil {
		return nil
	}

	voteCopy := *vote

	voteCopy.BlockID = vote.BlockID.Copy()
	voteCopy.ValidatorProTxHash = vote.ValidatorProTxHash.Copy()
	voteCopy.BlockSignature = vote.BlockSignature.Copy()
	voteCopy.VoteExtensions = vote.VoteExtensions.Copy()

	return &voteCopy
}

// String returns a string representation of Vote.
//
// 1. validator index
// 2. first 6 bytes of validator proTxHash
// 3. height
// 4. round,
// 5. type byte
// 6. type string
// 7. first 6 bytes of block hash
// 8. first 6 bytes of signature
// 9. first 6 bytes of vote extension
// 10. timestamp
func (vote *Vote) String() string {
	if vote == nil {
		return absentVoteStr
	}

	var typeString string
	switch vote.Type {
	case tmproto.PrevoteType:
		typeString = "Prevote"
	case tmproto.PrecommitType:
		typeString = "Precommit"
	default:
		panic("Unknown vote type")
	}

	blockHashString := nilVoteStr
	if len(vote.BlockID.Hash) > 0 {
		blockHashString = fmt.Sprintf("%X", tmbytes.Fingerprint(vote.BlockID.Hash))
	}

	return fmt.Sprintf("Vote{%v:%X %v/%02d/%v(%s) %X %X}",
		vote.ValidatorIndex,
		tmbytes.Fingerprint(vote.ValidatorProTxHash),
		vote.Height,
		vote.Round,
		typeString,
		blockHashString,
		tmbytes.Fingerprint(vote.BlockSignature),
		vote.VoteExtensions.Fingerprint(),
	)
}

// Verify performs vote signature verification. It checks whether the block signature
// and vote extensions signatures correspond to the given chain ID and public key.
func (vote *Vote) Verify(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	pubKey crypto.PubKey,
	proTxHash ProTxHash,
) error {
	quorumSignData, err := MakeQuorumSigns(chainID, quorumType, quorumHash, vote.ToProto())
	if err != nil {
		return err
	}
	err = vote.verifyBasic(proTxHash, pubKey)
	if err != nil {
		return err
	}

	return quorumSignData.Verify(pubKey, vote.makeQuorumSigns())
}

func (vote *Vote) verifyBasic(proTxHash ProTxHash, pubKey crypto.PubKey) error {
	if !bytes.Equal(proTxHash, vote.ValidatorProTxHash) {
		return ErrVoteInvalidValidatorProTxHash
	}

	if pubKey == nil {
		return ErrVoteMissingValidatorPubKey
	}

	if len(pubKey.Bytes()) != bls12381.PubKeySize {
		return ErrVoteInvalidValidatorPubKeySize
	}

	if vote.Type != tmproto.PrecommitType && vote.VoteExtensions.Len() > 0 {
		return ErrVoteInvalidExtension
	}

	return nil
}

// VerifyExtensionSign checks whether the vote extension signature corresponds to the
// given chain ID and public key.
func (vote *Vote) VerifyExtensionSign(chainID string, pubKey crypto.PubKey, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash) error {
	if vote.Type != tmproto.PrecommitType || vote.BlockID.IsNil() {
		return nil
	}
	quorumSignData, err := MakeQuorumSigns(chainID, quorumType, quorumHash, vote.ToProto())
	if err != nil {
		return err
	}

	return quorumSignData.VerifyVoteExtensions(pubKey, vote.makeQuorumSigns())
}

func (vote *Vote) makeQuorumSigns() QuorumSigns {
	return QuorumSigns{
		BlockSign: vote.BlockSignature,
		VoteExtensionSignatures: vote.VoteExtensions.Filter(func(ext VoteExtensionIf) bool {
			return ext.IsThresholdRecoverable()
		}).GetSignatures(),
	}
}

// ValidateBasic checks whether the vote is well-formed. It does not, however,
// check vote extensions - for vote validation with vote extension validation,
// use ValidateWithExtension.
func (vote *Vote) ValidateBasic() error {
	if !IsVoteTypeValid(vote.Type) {
		return errors.New("invalid Type")
	}

	if vote.Height < 0 {
		return errors.New("negative Height")
	}

	if vote.Round < 0 {
		return errors.New("negative Round")
	}

	// NOTE: Timestamp validation is subtle and handled elsewhere.

	if err := vote.BlockID.ValidateBasic(); err != nil {
		return fmt.Errorf("wrong BlockID: %w", err)
	}

	// BlockID.ValidateBasic would not err if we for instance have an empty hash but a
	// non-empty PartsSetHeader:
	if !vote.BlockID.IsNil() && !vote.BlockID.IsComplete() {
		return fmt.Errorf("blockID must be either empty or complete, got: %v", vote.BlockID)
	}

	if len(vote.ValidatorProTxHash) != crypto.DefaultHashSize {
		return fmt.Errorf("expected ValidatorProTxHash size to be %d bytes, got %d bytes (%X)",
			crypto.DefaultHashSize,
			len(vote.ValidatorProTxHash),
			vote.ValidatorProTxHash.Bytes(),
		)
	}
	if vote.ValidatorIndex < 0 {
		return errors.New("negative ValidatorIndex")
	}
	if len(vote.BlockSignature) == 0 {
		return errors.New("block signature is missing")
	}

	if len(vote.BlockSignature) > SignatureSize {
		return fmt.Errorf("block signature is too big (max: %d)", SignatureSize)
	}

	// We should only ever see vote extensions in precommits.
	if vote.Type != tmproto.PrecommitType || (vote.Type == tmproto.PrecommitType && vote.BlockID.IsNil()) {
		if !vote.VoteExtensions.IsEmpty() {
			return errors.New("unexpected vote extensions")
		}
	}

	if vote.Type == tmproto.PrecommitType && !vote.BlockID.IsNil() {
		if err := vote.VoteExtensions.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// ValidateWithExtension performs the same validations as ValidateBasic, but
// additionally checks whether a vote extension signature is present. This
// function is used in places where vote extension signatures are expected.
func (vote *Vote) ValidateWithExtension() error {
	if err := vote.ValidateBasic(); err != nil {
		return err
	}

	// We should always see vote extension signatures in precommits
	if vote.Type == tmproto.PrecommitType {
		// TODO(thane): Remove extension length check once
		//              https://github.com/tendermint/tendermint/issues/8272 is
		//              resolved.
		return vote.VoteExtensions.Validate()
	}

	return nil
}

// ToProto converts the handwritten type to proto generated type
// return type, nil if everything converts safely, otherwise nil, error
func (vote *Vote) ToProto() *tmproto.Vote {
	if vote == nil {
		return nil
	}
	var voteExtensions []*tmproto.VoteExtension
	if vote.VoteExtensions != nil {
		voteExtensions = vote.VoteExtensions.ToProto()
	}
	return &tmproto.Vote{
		Type:               vote.Type,
		Height:             vote.Height,
		Round:              vote.Round,
		BlockID:            vote.BlockID.ToProto(),
		ValidatorProTxHash: vote.ValidatorProTxHash,
		ValidatorIndex:     vote.ValidatorIndex,
		BlockSignature:     vote.BlockSignature,
		VoteExtensions:     voteExtensions,
	}
}

// MarshalZerologObject formats this object for logging purposes
func (vote *Vote) MarshalZerologObject(e *zerolog.Event) {
	if vote == nil {
		return
	}
	e.Int64("height", vote.Height)
	e.Int32("round", vote.Round)
	e.Str("type", vote.Type.String())
	e.Str("block_id", vote.BlockID.String())
	e.Str("block_signature", vote.BlockSignature.ShortString())
	e.Str("val_proTxHash", vote.ValidatorProTxHash.ShortString())
	e.Int32("val_index", vote.ValidatorIndex)
	e.Bool("nil", vote.BlockID.IsNil())
	e.Array("extensions", vote.VoteExtensions)
}

func (vote *Vote) HasVoteMessage() *tmcons.HasVote {
	return &tmcons.HasVote{
		Height: vote.Height,
		Round:  vote.Round,
		Type:   vote.Type,
		Index:  vote.ValidatorIndex,
	}
}

func VoteBlockRequestIDProto(vote *tmproto.Vote) []byte {
	return heightRoundRequestID("dpbvote", vote.Height, vote.Round)
}

func heightRoundRequestID(prefix string, height int64, round int32) []byte {
	reqID := []byte(prefix)
	heightBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(heightBytes, uint64(height))
	roundBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(roundBytes, uint32(round))
	reqID = append(reqID, heightBytes...)
	reqID = append(reqID, roundBytes...)
	return crypto.Checksum(reqID)
}
