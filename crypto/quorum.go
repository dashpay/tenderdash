package crypto

import (
	"bytes"
	"fmt"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/dashpay/dashd-go/btcjson"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/rs/zerolog"
)

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
	if len(i.ID) != DefaultHashSize {
		return fmt.Errorf("invalid request ID size: %X", i.ID)
	}
	if len(i.MsgHash) != DefaultHashSize {
		return fmt.Errorf("invalid hash size %d: %X", len(i.MsgHash), i.MsgHash)
	}
	if len(i.QuorumHash) != DefaultHashSize {
		return fmt.Errorf("invalid quorum hash size %d: %X", len(i.QuorumHash), i.QuorumHash)
	}
	// Msg is optional
	if len(i.Msg) > 0 {
		if !bytes.Equal(Checksum(i.Msg), i.MsgHash) {
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
	e.Uint8("llmqType", uint8(i.LlmqType))

}

// NewSignItem creates a new instance of SignItem with calculating a hash for a raw and creating signID
//
// Arguments:
// - quorumType: quorum type
// - quorumHash: quorum hash
// - reqID: sign request ID
// - msg: raw data to be signed; it will be hashed with crypto.Checksum()
func NewSignItem(quorumType btcjson.LLMQType, quorumHash, reqID, msg []byte) SignItem {
	msgHash := Checksum(msg) // FIXME: shouldn't we use sha256(sha256(raw)) here?
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
