package types

import (
	"bytes"
	"fmt"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

// QuorumSignData holds data which is necessary for signing and verification block, state, and each vote-extension in a list
type QuorumSignData struct {
	Block                  SignItem
	VoteExtensionSignItems []SignItem
}

// Signs items inside QuorumSignData using a given private key.
//
// Mainly for testing.
func (q QuorumSignData) SignWithPrivkey(key crypto.PrivKey) (QuorumSigns, error) {
	var err error
	var signs QuorumSigns
	if signs.BlockSign, err = key.SignDigest(q.Block.SignHash); err != nil {
		return signs, err
	}

	signs.VoteExtensionSignatures = make([][]byte, 0, len(q.VoteExtensionSignItems))
	for _, item := range q.VoteExtensionSignItems {
		var sign []byte
		if sign, err = key.SignDigest(item.SignHash); err != nil {
			return signs, err
		}
		signs.VoteExtensionSignatures = append(signs.VoteExtensionSignatures, sign)
	}

	return signs, nil
}

// Verify verifies a block and threshold vote extensions quorum signatures.
// It needs quorum to be reached so that we have enough signatures to verify.
func (q QuorumSignData) Verify(pubKey crypto.PubKey, signatures QuorumSigns) error {
	var err error
	if err1 := q.VerifyBlock(pubKey, signatures); err1 != nil {
		err = multierror.Append(err, err1)
	}

	if err1 := q.VerifyVoteExtensions(pubKey, signatures); err1 != nil {
		err = multierror.Append(err, err1)
	}

	return err
}

// VerifyBlock verifies block signature
func (q QuorumSignData) VerifyBlock(pubKey crypto.PubKey, signatures QuorumSigns) error {
	if !q.Block.VerifySignature(pubKey, signatures.BlockSign) {
		return ErrVoteInvalidBlockSignature
	}

	return nil
}

// VerifyVoteExtensions verifies threshold vote extensions signatures
func (q QuorumSignData) VerifyVoteExtensions(pubKey crypto.PubKey, signatures QuorumSigns) error {
	if len(q.VoteExtensionSignItems) != len(signatures.VoteExtensionSignatures) {
		return fmt.Errorf("count of vote extension signatures (%d) doesn't match recoverable vote extensions (%d)",
			len(signatures.VoteExtensionSignatures), len(q.VoteExtensionSignItems),
		)
	}

	var err error
	for i, signItem := range q.VoteExtensionSignItems {
		if !signItem.VerifySignature(pubKey, signatures.VoteExtensionSignatures[i]) {
			err = multierror.Append(err, fmt.Errorf("vote-extension %d signature is invalid: pubkey %X, raw msg: %X, sigHash: %X, signature %X",
				i,
				pubKey.Bytes(),
				signItem.Msg,
				signItem.MsgHash,
				signatures.VoteExtensionSignatures[i],
			))
		}
	}

	return err
}

// MakeQuorumSignsWithVoteSet creates and returns QuorumSignData struct built with a vote-set and an added vote
func MakeQuorumSignsWithVoteSet(voteSet *VoteSet, vote *types.Vote) (QuorumSignData, error) {
	return MakeQuorumSigns(
		voteSet.chainID,
		voteSet.valSet.QuorumType,
		voteSet.valSet.QuorumHash,
		vote,
	)
}

// MakeQuorumSigns builds signing data for block, state and vote-extensions
// each a sign-id item consist of request-id, raw data, hash of raw and id
func MakeQuorumSigns(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	protoVote *types.Vote,
) (QuorumSignData, error) {
	quorumSign := QuorumSignData{
		Block: MakeBlockSignItem(chainID, protoVote, quorumType, quorumHash),
	}
	var err error
	quorumSign.VoteExtensionSignItems, err =
		VoteExtensionsFromProto(protoVote.VoteExtensions...).
			Filter(func(ext VoteExtensionIf) bool {
				return ext.IsThresholdRecoverable()
			}).
			SignItems(chainID, quorumType, quorumHash, protoVote.Height, protoVote.Round)
	if err != nil {
		return QuorumSignData{}, err
	}
	return quorumSign, nil
}

// MakeBlockSignItem creates SignItem struct for a block
func MakeBlockSignItem(chainID string, vote *types.Vote, quorumType btcjson.LLMQType, quorumHash []byte) SignItem {
	reqID := BlockRequestID(vote.Height, vote.Round)
	raw, err := vote.SignBytes(chainID)
	if err != nil {
		panic(fmt.Errorf("block sign item: %w", err))
	}
	return NewSignItem(quorumType, quorumHash, reqID, raw)
}

// BlockRequestID returns a block request ID
func BlockRequestID(height int64, round int32) []byte {
	return heightRoundRequestID("dpbvote", height, round)
}

// SignItem represents signing session data (in field SignItem.ID) that will be signed to get threshold signature share.
// Field names are the same as in Dash Core, but the meaning is different.
// See DIP-0007
type SignItem struct {
	LlmqType   btcjson.LLMQType // Quorum type for which this sign item is created
	ID         []byte           // Request ID for quorum signing
	MsgHash    []byte           // Checksum of Raw
	QuorumHash []byte           // Quorum hash for which this sign item is created

	SignHash []byte // Hash of llmqType, quorumHash, id, and msgHash - as provided to crypto sign/verify functions

	Msg []byte // Raw data to be signed, before any transformations; optional
}

// Validate validates prepared data for signing
func (i *SignItem) Validate() error {
	if len(i.ID) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid request ID size: %X", i.ID)
	}
	if len(i.MsgHash) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid hash size %d: %X", len(i.MsgHash), i.MsgHash)
	}
	if len(i.QuorumHash) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid quorum hash size %d: %X", len(i.QuorumHash), i.QuorumHash)
	}
	// Msg is optional
	if len(i.Msg) > 0 {
		if !bytes.Equal(crypto.Checksum(i.Msg), i.MsgHash) {
			return fmt.Errorf("invalid hash %X for raw data: %X", i.MsgHash, i.Msg)
		}
	}
	return nil
}

func (i SignItem) MarshalZerologObject(e *zerolog.Event) {
	e.Hex("msg", i.Msg)
	e.Hex("signRequestID", i.ID)
	e.Hex("signID", i.SignHash)
	e.Hex("msgHash", i.MsgHash)
	e.Hex("quorumHash", i.QuorumHash)
	e.Int("llmqType", int(i.LlmqType))

}

// NewSignItem creates a new instance of SignItem with calculating a hash for a raw and creating signID
//
// Arguments:
// - quorumType: quorum type
// - quorumHash: quorum hash
// - reqID: sign request ID
// - msg: raw data to be signed; it will be hashed with crypto.Checksum()
func NewSignItem(quorumType btcjson.LLMQType, quorumHash, reqID, msg []byte) SignItem {
	msgHash := crypto.Checksum(msg) // FIXME: shouldn't we use sha256(sha256(raw)) here?
	item := NewSignItemFromHash(quorumType, quorumHash, reqID, msgHash)
	item.Msg = msg

	return item
}

// Create a new sign item without raw value, using provided hash.
func NewSignItemFromHash(quorumType btcjson.LLMQType, quorumHash, reqID, msgHash []byte) SignItem {
	item := SignItem{
		ID:         reqID,
		MsgHash:    msgHash,
		LlmqType:   quorumType,
		QuorumHash: quorumHash,
		Msg:        nil, // Raw is empty, as we don't have it
	}

	// By default, reverse fields when calculating SignHash
	item.UpdateSignHash(true)

	return item
}

// UpdateSignHash recalculates signHash field
// If reverse is true, then all []byte elements will be reversed before
// calculating signID
func (i *SignItem) UpdateSignHash(reverse bool) {
	if err := i.Validate(); err != nil {
		panic("invalid sign item: " + err.Error())
	}
	llmqType := i.LlmqType

	quorumHash := i.QuorumHash
	requestID := i.ID
	messageHash := i.MsgHash

	if reverse {
		quorumHash = tmbytes.Reverse(quorumHash)
		requestID = tmbytes.Reverse(requestID)
		messageHash = tmbytes.Reverse(messageHash)
	}

	var blsQuorumHash bls.Hash
	copy(blsQuorumHash[:], quorumHash)

	var blsRequestID bls.Hash
	copy(blsRequestID[:], requestID)

	var blsMessageHash bls.Hash
	copy(blsMessageHash[:], messageHash)

	// fmt.Printf("LlmqType: %x + ", llmqType)
	// fmt.Printf("QuorumHash: %x + ", blsQuorumHash)
	// fmt.Printf("RequestID: %x + ", blsRequestID)
	// fmt.Printf("MsgHash: %x\n", blsMessageHash)

	blsSignHash := bls.BuildSignHash(uint8(llmqType), blsQuorumHash, blsRequestID, blsMessageHash)

	signHash := make([]byte, 32)
	copy(signHash, blsSignHash[:])

	i.SignHash = signHash
}

// VerifySignature verifies signature for a sign item
func (i *SignItem) VerifySignature(pubkey crypto.PubKey, sig []byte) bool {
	return pubkey.VerifySignatureDigest(i.SignHash, sig)
}
