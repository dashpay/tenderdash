package types

import (
	"fmt"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/libs/protoio"
	"github.com/tendermint/tendermint/libs/bytes"
)

// IsZero returns true when the object is a zero-value or nil
func (m *BlockID) IsZero() bool {
	return m == nil || (len(m.Hash) == 0 && m.PartSetHeader.IsZero() && len(m.StateID) == 0)
}

func (m *BlockID) ToCanonicalBlockID() *CanonicalBlockID {
	if m == nil || m.IsZero() {
		return nil
	}
	cbid := CanonicalBlockID{
		Hash:          m.Hash,
		PartSetHeader: m.PartSetHeader.ToCanonicalPartSetHeader(),
		StateID:       m.StateID,
	}

	return &cbid
}

func (m *PartSetHeader) ToCanonicalPartSetHeader() CanonicalPartSetHeader {
	if m == nil || m.IsZero() {
		return CanonicalPartSetHeader{}
	}
	cps := CanonicalPartSetHeader(*m)
	return cps
}

// IsZero returns true when the object is a zero-value or nil
func (m *PartSetHeader) IsZero() bool {
	return m == nil || len(m.Hash) == 0
}

// VoteExtensionsToMap creates a map where a key is vote-extension type and value is the extensions grouped by type
func (m *Vote) VoteExtensionsToMap() VoteExtensions {
	if m == nil {
		return nil
	}
	res := make(map[VoteExtensionType][]*VoteExtension)
	for _, ext := range m.VoteExtensions {
		res[ext.Type] = append(res[ext.Type], ext)
	}
	return res
}

type voteSignBytes struct {
	// CanonicalVoteID is a crypto.Checksum of `CanonicalVote` encoded using protobuf encoding
	CanonicalVoteID [crypto.HashSize]byte
	// CanonicalVoteID is a crypto.Checksum of `StateID` encoded using raw fixed-length encoding.
	// It's filled with 0s for NIL-votes
	StateID [crypto.HashSize]byte
}

// SignBytes represent data to be signed for the given vote.
// It's a 64-byte slice containing concatenation of:
// * Checksum of CanonicalVote
// * Checksum of StateID
func (m Vote) SignBytes(chainID string) ([]byte, error) {
	var signBytes voteSignBytes

	pbVote := m.ToCanonicalVote(chainID)
	marshaledVote, err := protoio.MarshalDelimited(&pbVote)
	if err != nil {
		return nil, fmt.Errorf("vote SignBytes: %w", err)
	}
	canonicalVoteID := crypto.Checksum(marshaledVote)
	signBytes.CanonicalVoteID = *(*[crypto.HashSize]byte)(canonicalVoteID)

	if !m.BlockID.IsZero() {
		signBytes.StateID = *(*[crypto.HashSize]byte)(m.BlockID.StateID)
	}

	return bytes.MarshalFixedSize(signBytes)
}

// CanonicalizeVote transforms the given Vote to a CanonicalVote, which does
// not contain ValidatorIndex and ValidatorProTxHash fields.
func (m Vote) ToCanonicalVote(chainID string) CanonicalVote {
	return CanonicalVote{
		Type:    m.Type,
		Height:  m.Height,       // encoded as sfixed64
		Round:   int64(m.Round), // encoded as sfixed64
		BlockID: m.BlockID.ToCanonicalBlockID(),
		ChainID: chainID,
	}
}
