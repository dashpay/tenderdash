package consensus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestRoundSkipCertificateWanted(t *testing.T) {
	valSet, _ := types.RandValidatorSet(4)
	stateDataAt := func(version tmproto.VersionParams_ConsensusVersion, round int32, step cstypes.RoundStepType) *StateData {
		return &StateData{
			RoundState: cstypes.RoundState{Height: 7, Round: round, Step: step, Validators: valSet},
			state: sm.State{ConsensusParams: types.ConsensusParams{
				Version: types.VersionParams{ConsensusVersion: int32(version)},
			}},
		}
	}
	certificate := func(height int64, round int32) *types.RoundSkipCertificate {
		return &types.RoundSkipCertificate{Height: height, Round: round}
	}
	v2 := tmproto.VersionParams_CONSENSUS_VERSION_2

	testCases := map[string]struct {
		stateData   *StateData
		certificate *types.RoundSkipCertificate
		want        bool
	}{
		"should want one for the round the node waits in": {
			stateData:   stateDataAt(v2, 2, cstypes.RoundStepNewRound),
			certificate: certificate(7, 2),
			want:        true,
		},
		"should want one for a later round": {
			stateData:   stateDataAt(v2, 2, cstypes.RoundStepPrevote),
			certificate: certificate(7, 4),
			want:        true,
		},
		"should not want one before CONSENSUS_VERSION_2, so none reaches the WAL": {
			stateData:   stateDataAt(tmproto.VersionParams_CONSENSUS_VERSION_1, 2, cstypes.RoundStepNewRound),
			certificate: certificate(7, 2),
		},
		"should not want one for another height": {
			stateData:   stateDataAt(v2, 2, cstypes.RoundStepNewRound),
			certificate: certificate(8, 2),
		},
		"should not want one for a round the node has left": {
			stateData:   stateDataAt(v2, 2, cstypes.RoundStepNewRound),
			certificate: certificate(7, 1),
		},
		"should not want one while committing the height": {
			stateData:   stateDataAt(v2, 2, cstypes.RoundStepApplyCommit),
			certificate: certificate(7, 2),
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, roundSkipCertificateWanted(tc.stateData, tc.certificate))
		})
	}

	t.Run("should not want one where the threshold is raised beyond what it proves", func(t *testing.T) {
		raised := valSet.Copy()
		raised.VotingPowerThreshold = uint64(raised.TotalVotingPower())
		require.Greater(t, raised.QuorumVotingThresholdPower(), raised.QuorumTypeThresholdVotingPower())
		stateData := stateDataAt(v2, 2, cstypes.RoundStepNewRound)
		stateData.Validators = raised
		assert.False(t, roundSkipCertificateWanted(stateData, certificate(7, 2)))
	})
}
