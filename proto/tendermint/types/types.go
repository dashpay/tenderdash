package types

import (
	"fmt"

	"github.com/tendermint/tendermint/crypto"
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

// SignBytes represent data to be signed for the given vote.
// It's a 64-byte slice containing concatenation of:
// * Checksum of CanonicalVote
// * Checksum of StateID
func (m Vote) SignBytes(chainID string) ([]byte, error) {
	pbVote, err := m.ToCanonicalVote(chainID)
	if err != nil {
		return nil, err
	}
	return bytes.MarshalFixedSize(pbVote)
}

// CanonicalizeVote transforms the given Vote to a CanonicalVote, which does
// not contain ValidatorIndex and ValidatorProTxHash fields.
func (m Vote) ToCanonicalVote(chainID string) (CanonicalVote, error) {
	var (
		blockIDBytes []byte
		stateIDBytes []byte
		err          error
	)
	if !m.BlockID.IsZero() {
		blockID := m.BlockID.ToCanonicalBlockID()
		if blockID != nil {
			if blockIDBytes, err = blockID.SignBytes(); err != nil {
				return CanonicalVote{}, err
			}
		}
		blockIDBytes = crypto.Checksum(blockIDBytes)
		stateIDBytes = m.BlockID.StateID
	} else {
		blockIDBytes = crypto.Checksum([]byte{})
		stateIDBytes = crypto.Checksum([]byte{})
	}

	// We must ensure length is correct before doing fixed-len marshalling
	if len(blockIDBytes) != crypto.HashSize {
		return CanonicalVote{}, fmt.Errorf("block ID hash %x invalid length", blockIDBytes)
	}
	if len(stateIDBytes) != crypto.HashSize {
		return CanonicalVote{}, fmt.Errorf("state ID hash %x invalid length", stateIDBytes)
	}

	return CanonicalVote{
		Type:    m.Type,
		Height:  m.Height,       // encoded as sfixed64
		Round:   int64(m.Round), // encoded as sfixed64
		BlockID: blockIDBytes,
		StateID: stateIDBytes,
		ChainID: chainID,
	}, nil
}
