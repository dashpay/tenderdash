package types

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

var (
	errUnexpectedVoteType = errors.New("unexpected vote extension - vote extensions are only allowed in precommits")
)

// QuorumSignData holds data which is necessary for signing and verification block, state, and each vote-extension in a list
type QuorumSignData struct {
	Block      SignItem
	Extensions map[types.VoteExtensionType][]SignItem
}

// Verify verifies a quorum signatures: block, state and vote-extensions
func (q QuorumSignData) Verify(pubKey crypto.PubKey, signs QuorumSigns) error {
	return NewQuorumSignsVerifier(q).Verify(pubKey, signs)
}

// SignItem represents quorum sign data, like a request id, message bytes, sha256 hash of message and signID
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
	if len(i.ReqID) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid request ID size: %X", i.ReqID)
	}
	if len(i.Hash) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid hash size: %X", i.ReqID)
	}
	if len(i.QuorumHash) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid quorum hash size: %X", i.ReqID)
	}
	if len(i.Raw) > 0 {
		if !bytes.Equal(crypto.Checksum(i.Raw), i.Hash) {
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
	quorumSign.Extensions, err = MakeVoteExtensionSignItems(chainID, protoVote, quorumType, quorumHash)
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

// MakeVoteExtensionSignItems  creates a list SignItem structs for a vote extensions
func MakeVoteExtensionSignItems(
	chainID string,
	protoVote *types.Vote,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
) (map[types.VoteExtensionType][]SignItem, error) {
	// We only sign vote extensions for precommits
	if protoVote.Type != types.PrecommitType {
		if len(protoVote.VoteExtensions) > 0 {
			return nil, errUnexpectedVoteType
		}
		return nil, nil
	}
	items := make(map[types.VoteExtensionType][]SignItem)
	protoExtensionsMap := protoVote.VoteExtensionsToMap()
	for t, exts := range protoExtensionsMap {
		if items[t] == nil && len(exts) > 0 {
			items[t] = make([]SignItem, len(exts))
		}

		for i, ext := range exts {
			reqID, err := VoteExtensionRequestID(ext, protoVote.Height, protoVote.Round)
			if err != nil {
				return nil, err
			}
			raw := VoteExtensionSignBytes(chainID, protoVote.Height, protoVote.Round, ext)
			// TODO: this is to avoid sha256 of raw data, to be removed once we get into agreement on the format
			if ext.Type == types.VoteExtensionType_THRESHOLD_RECOVER_RAW {
				// for this vote extension type, we just sign raw data from extension
				msgHash := bytes.Clone(raw)
				items[t][i] = NewSignItemFromHash(quorumType, quorumHash, reqID, msgHash)
				items[t][i].Raw = raw
			} else {
				items[t][i] = NewSignItem(quorumType, quorumHash, reqID, raw)
			}
		}
	}
	return items, nil
}

// NewSignItem creates a new instance of SignItem with calculating a hash for a raw and creating signID
//
// Arguments:
// - quorumType: quorum type
// - quorumHash: quorum hash
// - reqID: sign request ID
// - raw: raw data to be signed; it will be hashed with crypto.Checksum()
func NewSignItem(quorumType btcjson.LLMQType, quorumHash, reqID, raw []byte) SignItem {
	msgHash := crypto.Checksum(raw)
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

	if testing.Testing() {
		fmt.Printf("generating  sign ID using bls.BuildSignHash for %d %X %X %X\n", i.QuorumType, quorumHash, requestID, messageHash)
		out := append([]byte{byte(i.QuorumType)}, quorumHash...)
		out = append(out, requestID...)
		out = append(out, messageHash...)

		fmt.Printf("data before sha256: %X\n", out)
		fmt.Printf("sha256(sha256(data)): %X\n", crypto.Checksum((crypto.Checksum(out))))

	}
	i.ID = crypto.SignID(
		i.QuorumType,
		quorumHash,
		requestID,
		messageHash,
	)
}
