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
	ID         []byte           // Request ID for quorum signing
	SignHash   []byte           // Hash of llmqType, quorumHash, id, and msgHash - final data to  sign/verify
	Raw        []byte           // Raw data to be signed, before any transformations
	RawHash    []byte           // Checksum of Raw
	LlmqType   btcjson.LLMQType // Quorum type for which this sign item is created
	QuorumHash []byte           // Quorum hash for which this sign item is created
}

// Validate validates prepared data for signing
func (i *SignItem) Validate() error {
	if len(i.ID) != DefaultHashSize {
		return fmt.Errorf("invalid request ID size: %X", i.ID)
	}
	if len(i.RawHash) != DefaultHashSize {
		return fmt.Errorf("invalid hash size %d: %X", len(i.RawHash), i.RawHash)
	}
	if len(i.QuorumHash) != DefaultHashSize {
		return fmt.Errorf("invalid quorum hash size %d: %X", len(i.QuorumHash), i.QuorumHash)
	}
	if len(i.Raw) > 0 {
		if !bytes.Equal(Checksum(i.Raw), i.RawHash) {
			return fmt.Errorf("invalid hash %X for raw data: %X", i.RawHash, i.Raw)
		}
	}
	return nil
}

func (i SignItem) MarshalZerologObject(e *zerolog.Event) {
	e.Hex("signBytes", i.Raw)
	e.Hex("signRequestID", i.ID)
	e.Hex("signID", i.SignHash)
	e.Hex("signHash", i.RawHash)
}

// NewSignItem creates a new instance of SignItem with calculating a hash for a raw and creating signID
//
// Arguments:
// - quorumType: quorum type
// - quorumHash: quorum hash
// - reqID: sign request ID
// - raw: raw data to be signed; it will be hashed with crypto.Checksum()
func NewSignItem(quorumType btcjson.LLMQType, quorumHash, reqID, raw []byte) SignItem {
	msgHash := Checksum(raw)
	item := NewSignItemFromHash(quorumType, quorumHash, reqID, msgHash)
	item.Raw = raw

	return item
}

// Create a new sign item without raw value, using provided hash.
func NewSignItemFromHash(quorumType btcjson.LLMQType, quorumHash, reqID, msgHash []byte) SignItem {
	item := SignItem{
		ID:         reqID,
		RawHash:    msgHash,
		LlmqType:   quorumType,
		QuorumHash: quorumHash,
		Raw:        nil, // Raw is empty, as we don't have it
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
	messageHash := i.RawHash

	if reverse {
		quorumHash = tmbytes.Reverse(quorumHash)
		requestID = tmbytes.Reverse(requestID)
		messageHash = tmbytes.Reverse(messageHash)
	}

	// if testing.Testing() {
	// 	fmt.Printf("generating  sign ID using bls.BuildSignHash for %d %X %X %X\n", llmqType, quorumHash, requestID, messageHash)
	// 	out := append([]byte{byte(llmqType)}, quorumHash...)
	// 	out = append(out, requestID...)
	// 	out = append(out, messageHash...)

	// 	fmt.Printf("data before sha256: %X\n", out)
	// 	fmt.Printf("sha256(sha256(data)): %X\n", crypto.Checksum((crypto.Checksum(out))))
	// }

	var blsQuorumHash bls.Hash
	copy(blsQuorumHash[:], quorumHash)

	var blsRequestID bls.Hash
	copy(blsRequestID[:], requestID)

	var blsMessageHash bls.Hash
	copy(blsMessageHash[:], messageHash)

	blsSignHash := bls.BuildSignHash(uint8(llmqType), blsQuorumHash, blsRequestID, blsMessageHash)

	signHash := make([]byte, 32)
	copy(signHash, blsSignHash[:])

	i.SignHash = signHash
}
