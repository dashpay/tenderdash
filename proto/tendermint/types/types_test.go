package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/rand"
)

func TestVoteSignBytes(t *testing.T) {
	h := rand.Bytes(crypto.HashSize)
	v := Vote{
		Type:   PrecommitType,
		Height: 1,
		Round:  2,
		BlockID: BlockID{
			Hash:          h,
			PartSetHeader: PartSetHeader{Total: 1, Hash: h},
			StateID:       h,
		},
	}
	const chainID = "some-chain"
	sb, err := v.SignBytes(chainID)
	require.NoError(t, err)
	assert.Len(t, sb, 4+8+8+32+32+len(chainID)) // type(4) + height(8) + round(8) + blockID(32) + stateID(32)
}
