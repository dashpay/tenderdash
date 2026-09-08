package light_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

// A light-block provider must not be able to replace validator-set fields while
// preserving the ValidatorsHash authenticated by the header chain.
func TestSecurityLightBlockBindsValidatorSetState(t *testing.T) {
	const chainID = "security-validator-set-binding"
	headers, validatorSets, _ := genLightBlocksWithValidatorsRotatingEveryBlock(
		t, chainID, 1, 10, time.Now().Add(-time.Hour),
	)

	header := headers[1]
	honest := validatorSets[1]
	require.NotNil(t, header)
	require.NotNil(t, honest)

	tests := []struct {
		name   string
		mutate func(*testing.T, *types.ValidatorSet)
	}{
		{"proposer", func(t *testing.T, forged *types.ValidatorSet) {
			require.NoError(t, forged.SetProposer(forged.Validators[1].ProTxHash))
		}},
		{"voting_power_threshold", func(_ *testing.T, forged *types.ValidatorSet) {
			forged.VotingPowerThreshold = uint64(forged.TotalVotingPower())
		}},
		{"public_key_availability", func(_ *testing.T, forged *types.ValidatorSet) {
			forged.HasPublicKeys = false
		}},
		{"membership", func(t *testing.T, forged *types.ValidatorSet) {
			replacement, _ := types.RandValidatorSet(len(forged.Validators))
			forged.Validators = replacement.Validators
			forged.HasPublicKeys = false
			require.NoError(t, forged.SetProposer(forged.Validators[0].ProTxHash))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forged := honest.Copy()
			test.mutate(t, forged)

			assert.NotEqual(t, honest.Hash(), forged.Hash(),
				"ValidatorsHash must bind consensus-relevant validator-set state")
			lightBlock := &types.LightBlock{SignedHeader: header, ValidatorSet: forged}
			assert.Error(t, lightBlock.ValidateBasic(chainID),
				"full light-block validation must reject forged validator-set state")
		})
	}
}
