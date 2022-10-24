package types

import (
	"fmt"

	"github.com/tendermint/tendermint/internal/libs/protoio"
)

// IsZero returns true when the object is a zero-value or nil
func (m *BlockID) IsZero() bool {
	return m == nil || (len(m.Hash) == 0 && len(m.PartSetHeader.Hash) == 0)
}

func (m *BlockID) ToCanonicalBlockID() *CanonicalBlockID {
	if m == nil || m.IsZero() {
		return nil
	}
	cbid := CanonicalBlockID{
		Hash:          m.Hash,
		PartSetHeader: m.PartSetHeader.ToCanonicalPartSetHeader(),
		StateId:       m.StateId,
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

func (m Vote) SignBytes(chainID string) ([]byte, error) {
	pb := m.ToCanonicalVote(chainID)
	bz, err := protoio.MarshalDelimited(&pb)
	if err != nil {
		return nil, fmt.Errorf("vote SignBytes: %w", err)
	}

	return bz, nil
}
