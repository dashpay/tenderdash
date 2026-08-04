package statesync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/types"
)

// The consensus params a peer serves are persisted by Bootstrap, and their hash
// covers only Block.MaxBytes, Block.MaxGas and Version.ConsensusVersion. Values
// outside that cover must therefore be validated, and validated first: a peer that
// tampers with them also controls the hash it advertises.
func TestVerifyConsensusParams(t *testing.T) {
	const height = int64(7)

	valid := types.DefaultConsensusParams()
	require.NoError(t, valid.ValidateConsensusParams())

	invalid := types.DefaultConsensusParams()
	invalid.Timeout.Vote = -1

	testCases := []struct {
		name     string
		params   types.ConsensusParams
		hash     tmbytes.HexBytes
		errorIs  string
		expectOK bool
	}{
		{
			name:     "valid params with a matching hash",
			params:   *valid,
			hash:     valid.HashConsensusParams(),
			expectOK: true,
		},
		{
			name:    "valid params with a mismatched hash",
			params:  *valid,
			hash:    tmbytes.HexBytes("not the params hash"),
			errorIs: "consensus params hash mismatch",
		},
		{
			name:    "invalid params with a matching hash",
			params:  *invalid,
			hash:    invalid.HashConsensusParams(),
			errorIs: "invalid consensus params",
		},
		{
			name:    "invalid params with a mismatched hash",
			params:  *invalid,
			hash:    tmbytes.HexBytes("not the params hash"),
			errorIs: "invalid consensus params",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := verifyConsensusParams(tc.params, tc.hash, height)
			if tc.expectOK {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorIs)
		})
	}
}
