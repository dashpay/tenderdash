package state_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// TestTxFilterRandomBeacon checks that the pre-check reserves room for the last commit's beacon and
// app hash from consensus version 2, and only then.
func TestTxFilterRandomBeacon(t *testing.T) {
	const maxBlockBytes = 3241
	maxTxSize := maxBlockBytes - 1132
	genDoc := factory.MinimalGenesisDoc()
	genDoc.ConsensusParams.Block.MaxBytes = maxBlockBytes
	genDoc.ConsensusParams.Evidence.MaxBytes = 1500
	state, err := sm.MakeGenesisState(&genDoc)
	require.NoError(t, err)
	require.NoError(t, sm.TxPreCheckForState(state)(types.Tx(tmrand.Bytes(maxTxSize))))

	state.ConsensusParams.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
	f := sm.TxPreCheckForState(state)
	maxTxSize -= int(types.MaxCommitRandomnessBytes)
	require.NoError(t, f(types.Tx(tmrand.Bytes(maxTxSize))))
	require.Error(t, f(types.Tx(tmrand.Bytes(maxTxSize+1))))
}

func TestTxFilter(t *testing.T) {
	const maxBlockBytes = 3241
	maxTxSize := maxBlockBytes - 1132
	genDoc := factory.MinimalGenesisDoc()
	genDoc.ConsensusParams.Block.MaxBytes = maxBlockBytes
	genDoc.ConsensusParams.Evidence.MaxBytes = 1500

	// Max size of Txs is much smaller than size of block,
	// since we need to account for commits and evidence.

	testCases := []struct {
		bytes int
		isErr bool
	}{
		{0, false},
		{maxTxSize - 1, false},
		{maxTxSize, false},
		{maxTxSize + 1, true},
		{3000, true},
	}
	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			state, err := sm.MakeGenesisState(&genDoc)
			require.NoError(t, err)
			tx := types.Tx(tmrand.Bytes(tc.bytes))
			// Read MaxDataBytes, for debugging/logging only
			maxDataBytes, err := types.MaxDataBytes(state.ConsensusParams.Block.MaxBytes, nil, 0)
			require.NoError(t, err)

			f := sm.TxPreCheckForState(state)
			if tc.isErr {
				assert.NotNil(t, f(tx), "%+v, maxDataBytes:%d", tc, maxDataBytes)
			} else {
				assert.Nil(t, f(tx), "%+v, maxDataBytes:%d", tc, maxDataBytes)
			}
		})
	}
}
