package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/types"
)

// TestValsetUpdateOverlaysVotingPowerThreshold guards against a stale voting
// power threshold leaking into the live commit gate.
//
// valsetUpdate has three branches after currentVals.Copy() (which now preserves
// VotingPowerThreshold). Only the same-quorum-with-updates branch used to skip
// overlaying the new params.VotingPowerThreshold, so the returned set kept the
// previous height's threshold while NextConsensusParams recorded the new one —
// QuorumVotingThresholdPower() then gated commits on a stale value for one height.
//
// Each sub-case feeds a new threshold (150) over an old one (200) and asserts the
// returned set reflects the new threshold, both as the raw field and through the
// live gate QuorumVotingThresholdPower().
func TestValsetUpdateOverlaysVotingPowerThreshold(t *testing.T) {
	const (
		oldThreshold uint64 = 200 // 2 validators * DefaultDashVotingPower; total power == 200
		newThreshold uint64 = 150 // 0 < newThreshold <= totalPower, and != oldThreshold
	)

	newParams := func() types.ValidatorParams {
		th := newThreshold
		return types.ValidatorParams{
			PubKeyTypes:          []string{types.ABCIPubKeyTypeBLS12381},
			VotingPowerThreshold: &th,
		}
	}

	t.Run("same quorum with updates (branch 1, regression)", func(t *testing.T) {
		baseSet, _ := factory.MockValidatorSet()
		baseSet.VotingPowerThreshold = oldThreshold

		// Same quorum hash + non-empty validator updates forces branch 1.
		vsu := baseSet.ABCIEquivalentValidatorUpdates()

		got, err := valsetUpdate(vsu, baseSet, newParams())
		require.NoError(t, err)

		assert.Equal(t, newThreshold, got.VotingPowerThreshold,
			"branch 1 must overlay the new params threshold, not keep the copied old one")
		assert.Equal(t, int64(newThreshold), got.QuorumVotingThresholdPower(),
			"live commit gate must use the new threshold")
	})

	// The remaining sub-cases document that the other two branches already honored
	// the new threshold; they pin that behavior so the asymmetry cannot reappear.

	t.Run("no validator updates (else branch)", func(t *testing.T) {
		baseSet, _ := factory.MockValidatorSet()
		baseSet.VotingPowerThreshold = oldThreshold

		// Empty validator updates take the else branch.
		vsu := &abci.ValidatorSetUpdate{
			ThresholdPublicKey: baseSet.ABCIEquivalentValidatorUpdates().ThresholdPublicKey,
			QuorumHash:         baseSet.QuorumHash,
		}

		got, err := valsetUpdate(vsu, baseSet, newParams())
		require.NoError(t, err)

		assert.Equal(t, newThreshold, got.VotingPowerThreshold)
		assert.Equal(t, int64(newThreshold), got.QuorumVotingThresholdPower())
	})

	t.Run("new quorum with updates (branch 2)", func(t *testing.T) {
		baseSet, _ := factory.MockValidatorSet()
		baseSet.VotingPowerThreshold = oldThreshold

		vsu := baseSet.ABCIEquivalentValidatorUpdates()
		// A different quorum hash forces branch 2 (NewValidatorSetCheckPublicKeys).
		vsu.QuorumHash = crypto.QuorumHash(tmbytes.MustHexDecode(
			"AABBCCDDEEFF00112233445566778899AABBCCDDEEFF00112233445566778899"))

		got, err := valsetUpdate(vsu, baseSet, newParams())
		require.NoError(t, err)

		assert.Equal(t, newThreshold, got.VotingPowerThreshold)
		assert.Equal(t, int64(newThreshold), got.QuorumVotingThresholdPower())
	})
}
