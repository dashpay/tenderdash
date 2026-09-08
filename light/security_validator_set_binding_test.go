package light_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

// Light-block validation rejects fields that can be checked against the signed
// header or the threshold public key without changing the v1.7 block hash.
func TestSecurityLightBlockRejectsValidatorSetForgery(t *testing.T) {
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

			lightBlock := &types.LightBlock{SignedHeader: header, ValidatorSet: forged}
			assert.Error(t, lightBlock.ValidateBasic(chainID),
				"full light-block validation must reject forged validator-set state")
		})
	}
}
