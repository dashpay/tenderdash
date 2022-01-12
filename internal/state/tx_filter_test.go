package state_test

import (
	"testing"

	tmrand "github.com/tendermint/tendermint/libs/rand"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/types"
)

func TestTxFilter(t *testing.T) {
	genDoc := randomGenesisDoc()
	genDoc.ConsensusParams.Block.MaxBytes = 3241
	genDoc.ConsensusParams.Evidence.MaxBytes = 1500

	// Max size of Txs is much smaller than size of block,
	// since we need to account for commits and evidence.
	testCases := []struct {
		tx    types.Tx
		isErr bool
	}{
		{types.Tx(tmrand.Bytes(2120)), false},
		{types.Tx(tmrand.Bytes(2121)), true},
		{types.Tx(tmrand.Bytes(3000)), true},
	}
	// We get 2202 above as we have 80 more bytes in max bytes and we are using bls, so 2155 + 80 - 32 - 1 = 2202
	// The 32 is the signature difference size between edwards and bls
	// The 1 is the protobuf encoding difference because the sizes use signed integers and we are going from less
	// than 128 to over 128

	for i, tc := range testCases {
		state, err := sm.MakeGenesisState(genDoc)
		require.NoError(t, err)

		f := sm.TxPreCheck(state)
		if tc.isErr {
			assert.NotNil(t, f(tc.tx), "#%v", i)
		} else {
			assert.Nil(t, f(tc.tx), "#%v", i)
		}
	}
}
