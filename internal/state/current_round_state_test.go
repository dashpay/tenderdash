package state

import (
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/types"
)

func TestValsetUpdateVotingPowerThreshold(t *testing.T) {
	currentVals, _ := types.RandValidatorSet(4)
	currentVals.VotingPowerThreshold = 33

	threshold := uint64(55)
	params := types.ValidatorParams{PubKeyTypes: []string{"bls12381"}, VotingPowerThreshold: &threshold}

	removedVal := types.TM2PB.NewValidatorUpdate(currentVals.Validators[0].PubKey, 0,
		currentVals.Validators[0].ProTxHash, currentVals.Validators[0].NodeAddress.String())
	thresholdPubKey := encoding.MustPubKeyToProto(currentVals.ThresholdPublicKey)

	testCases := []struct {
		name   string
		update *abci.ValidatorSetUpdate
	}{
		{
			name: "same quorum",
			update: &abci.ValidatorSetUpdate{
				ValidatorUpdates:   []abci.ValidatorUpdate{removedVal},
				ThresholdPublicKey: thresholdPubKey,
				QuorumHash:         currentVals.QuorumHash,
			},
		},
		{
			name:   "new quorum",
			update: currentVals.ABCIEquivalentValidatorUpdates(),
		},
		{
			name: "no validator updates",
			update: &abci.ValidatorSetUpdate{
				ThresholdPublicKey: thresholdPubKey,
				QuorumHash:         currentVals.QuorumHash,
			},
		},
	}

	testCases[1].update.QuorumHash = append(testCases[1].update.QuorumHash[:0:0], testCases[1].update.QuorumHash...)
	testCases[1].update.QuorumHash[0]++

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			valSet, err := valsetUpdate(tc.update, currentVals, params)
			require.NoError(t, err)
			require.Equal(t, threshold, valSet.VotingPowerThreshold)
		})
	}
}
