package statesync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
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
	threshold := uint64(1)
	withUnsignedThreshold := *types.DefaultConsensusParams()
	withUnsignedThreshold.Validator.VotingPowerThreshold = &threshold

	testCases := []struct {
		name             string
		params           types.ConsensusParams
		hash             tmbytes.HexBytes
		trustedThreshold *uint64
		errorIs          string
		expectOK         bool
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
		{
			name:             "voting threshold matches trusted local state",
			params:           withUnsignedThreshold,
			hash:             withUnsignedThreshold.HashConsensusParams(),
			trustedThreshold: &threshold,
			expectOK:         true,
		},
		{
			name:    "voting threshold has no trusted local value",
			params:  withUnsignedThreshold,
			hash:    withUnsignedThreshold.HashConsensusParams(),
			errorIs: "does not match trusted local state",
		},
		{
			name:             "voting threshold differs from trusted local state",
			params:           *valid,
			hash:             valid.HashConsensusParams(),
			trustedThreshold: &threshold,
			errorIs:          "does not match trusted local state",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := verifyConsensusParams(tc.params, tc.hash, height, tc.trustedThreshold)
			if tc.expectOK {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorIs)
		})
	}
}

func TestApplyVotingPowerThreshold(t *testing.T) {
	const threshold = uint64(123)
	validators, _ := types.RandValidatorSet(4)
	lastValidators := validators.Copy()
	state := sm.State{
		Validators:     validators,
		LastValidators: lastValidators,
		ConsensusParams: types.ConsensusParams{
			Validator: types.ValidatorParams{VotingPowerThreshold: ptr(threshold)},
		},
	}

	applyVotingPowerThreshold(&state)

	require.Equal(t, threshold, state.Validators.VotingPowerThreshold)
	require.Equal(t, threshold, state.LastValidators.VotingPowerThreshold)
}

func ptr[T any](value T) *T {
	return &value
}
