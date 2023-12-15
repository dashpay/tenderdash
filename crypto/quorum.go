package crypto

import (
	"bytes"
	"fmt"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/rs/zerolog"
)

// SignItem represents signing session data (in field SignItem.ID) that will be signed to get threshold signature share.
// See DIP-0007
type SignItem struct {
	ReqID      []byte           // Request ID for quorum signing
	ID         []byte           // Signature ID
	Raw        []byte           // Raw data to be signed
	Hash       []byte           // Checksum of Raw
	QuorumType btcjson.LLMQType // Quorum type for which this sign item is created
	QuorumHash []byte           // Quorum hash for which this sign item is created
}

// Validate validates prepared data for signing
func (i *SignItem) Validate() error {
	if len(i.ReqID) != DefaultHashSize {
		return fmt.Errorf("invalid request ID size: %X", i.ReqID)
	}
	if len(i.Hash) != DefaultHashSize {
		return fmt.Errorf("invalid hash size: %X", i.ReqID)
	}
	if len(i.QuorumHash) != DefaultHashSize {
		return fmt.Errorf("invalid quorum hash size: %X", i.ReqID)
	}
	if len(i.Raw) > 0 {
		if !bytes.Equal(Checksum(i.Raw), i.Hash) {
			return fmt.Errorf("invalid hash %X for raw data: %X", i.Hash, i.Raw)
		}
	}
	return nil
}

func (i SignItem) MarshalZerologObject(e *zerolog.Event) {
	e.Hex("signBytes", i.Raw)
	e.Hex("signRequestID", i.ReqID)
	e.Hex("signID", i.ID)
	e.Hex("signHash", i.Hash)
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
		ReqID:      reqID,
		Hash:       msgHash,
		QuorumType: quorumType,
		QuorumHash: quorumHash,
		Raw:        nil, // Raw is empty, as we don't have it
	}
	item.UpdateID()

	return item
}

func (i *SignItem) UpdateID() {
	if err := i.Validate(); err != nil {
		panic("invalid sign item: " + err.Error())
	}
	// FIXME: previously we had reversals, but this doesn't work with Core test vectors
	// So
	// quorumHash := tmbytes.Reverse(i.QuorumHash)
	quorumHash := i.QuorumHash
	// requestID := tmbytes.Reverse(i.ReqID)
	requestID := i.ReqID
	// messageHash := tmbytes.Reverse(i.Hash)
	messageHash := i.Hash

	llmqType := i.QuorumType

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

	i.ID = signHash
}
