package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatorSetUpdatePublicKeyAvailability(t *testing.T) {
	complete, _ := RandValidatorSet(3)
	local := complete.Copy()
	for _, val := range local.Validators {
		val.PubKey = nil
	}
	local.HasPublicKeys = false
	require.NoError(t, local.UpdateWithChangeSet(complete.Validators[:1], complete.ThresholdPublicKey, complete.QuorumHash))
	require.False(t, local.HasPublicKeys, "one restored share is not enough")
	require.NoError(t, local.UpdateWithChangeSet(complete.Validators[1:], complete.ThresholdPublicKey, complete.QuorumHash))
	require.True(t, local.HasPublicKeys, "availability must include unchanged members")
	require.NoError(t, local.ValidateBasic())
	require.Equal(t, complete.Hash(), local.Hash())
	require.Equal(t, complete.Proposer().ProTxHash, local.Proposer().ProTxHash)
	missing := complete.Validators[0].Copy()
	missing.PubKey = nil
	require.NoError(t, local.UpdateWithChangeSet([]*Validator{missing}, complete.ThresholdPublicKey, complete.QuorumHash))
	require.False(t, local.HasPublicKeys)
	missing.VotingPower = 0
	require.NoError(t, local.UpdateWithChangeSet([]*Validator{missing}, complete.ThresholdPublicKey, complete.QuorumHash))
	require.True(t, local.HasPublicKeys, "removing the last keyless member restores availability")
}

func TestValidatorSetFromProtoRecomputesPublicKeyAvailability(t *testing.T) {
	for _, completeKeys := range []bool{true, false} {
		complete, _ := RandValidatorSet(3)
		local := complete.Copy()
		if !completeKeys {
			local.Validators[0].PubKey = nil
		}
		local.HasPublicKeys = false
		wire, err := local.ToProto()
		require.NoError(t, err)
		restored, err := ValidatorSetFromProto(wire)
		require.NoError(t, err)
		require.Equal(t, completeKeys, restored.HasPublicKeys)
		require.Equal(t, complete.Hash(), restored.Hash())
	}
}
