package state

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

func TestSameQuorumPublicKeyRefresh(t *testing.T) {
	for _, source := range []string{InitChainSource, PrepareProposalSource, ProcessProposalSource} {
		t.Run(source, func(t *testing.T) {
			complete, _ := factory.MockValidatorSet()
			params := *types.DefaultConsensusParams()
			params.Validator.VotingPowerThreshold = &complete.VotingPowerThreshold
			old := complete.Copy()
			old.QuorumHash = crypto.Checksum([]byte("previous quorum"))
			update := complete.ABCIEquivalentValidatorUpdates()
			for i := range update.ValidatorUpdates {
				update.ValidatorUpdates[i].PubKey = nil
			}
			base := State{InitialHeight: 1, LastBlockHeight: 524, Validators: old, ConsensusParams: params}
			first, err := NewCurrentRoundState(complete.Validators[0].ProTxHash,
				RoundParams{ValidatorSetUpdate: update, Source: source}, base)
			require.NoError(t, err)
			require.False(t, first.NextValidators.HasPublicKeys)
			require.NoError(t, first.UpdateState(&base))
			for _, refresh := range []bool{false, true} {
				if refresh {
					update = complete.ABCIEquivalentValidatorUpdates()
				}
				next, err := NewCurrentRoundState(complete.Validators[0].ProTxHash,
					RoundParams{ValidatorSetUpdate: update, Source: source}, base)
				require.NoError(t, err)
				require.Equal(t, refresh, next.NextValidators.HasPublicKeys)
				require.Equal(t, complete.Hash(), next.NextValidators.Hash())
				require.NoError(t, next.UpdateState(&base))
				require.Equal(t, refresh, base.Validators.HasPublicKeys)
			}
		})
	}
}

func TestSameQuorumPublicKeyRefreshValidatesShares(t *testing.T) {
	complete, _ := factory.MockValidatorSet()
	local := complete.Copy()
	for _, val := range local.Validators {
		val.PubKey = nil
	}
	local.HasPublicKeys = false
	complete.Validators[0].PubKey = bls12381.GenPrivKey().PubKey()
	_, err := valsetUpdate(complete.ABCIEquivalentValidatorUpdates(), local, types.ValidatorParams{
		PubKeyTypes: []string{types.ABCIPubKeyTypeBLS12381}, VotingPowerThreshold: &complete.VotingPowerThreshold,
	})
	require.Error(t, err, "a complete but inconsistent set of shares must be rejected")
	require.False(t, local.HasPublicKeys)
	require.Nil(t, local.Validators[0].PubKey)
}
