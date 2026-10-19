package statesync

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/statesync/mocks"
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

func TestStateProviderEvidenceMode(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after int64
		wantErr       bool
	}{
		{"cannot enable", 0, 1000, true}, {"cannot disable", 1000, 0, true},
		{"disabled snapshot", 0, 0, false}, {"enabled snapshot", 1000, 2000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := sm.State{ConsensusParams: *types.DefaultConsensusParams(), LastBlockHeight: 10}
			state.ConsensusParams.Evidence.MaxBytes = tc.after
			inner := mocks.NewStateProvider(t)
			inner.EXPECT().State(t.Context(), uint64(10)).Return(state, nil).Once()
			provider := evidenceModeStateProvider{
				StateProvider:  inner,
				evidenceParams: types.EvidenceParams{MaxBytes: tc.before},
			}
			restored, err := provider.State(t.Context(), 10)
			if tc.wantErr {
				require.ErrorContains(t, err, "restored state evidence mode differs")
				require.True(t, restored.IsEmpty())
			} else {
				require.NoError(t, err)
				require.Equal(t, state, restored)
			}
		})
	}
	inner := mocks.NewStateProvider(t)
	inner.EXPECT().State(t.Context(), uint64(10)).Return(sm.State{}, context.Canceled).Once()
	provider := evidenceModeStateProvider{StateProvider: inner}
	_, err := provider.State(t.Context(), 10)
	require.True(t, errors.Is(err, context.Canceled))
}
