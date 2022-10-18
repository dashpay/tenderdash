package types

import (
	"github.com/tendermint/tendermint/internal/libs/protoio"
)

func (v CanonicalVote) SignBytes() ([]byte, error) {
	return protoio.MarshalDelimited(&v)
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
