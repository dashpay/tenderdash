package statesync

import (
	"encoding/hex"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

// A state-sync provider must not be able to install validator membership or
// live consensus controls that the legacy ValidatorsHash does not authenticate.
func TestSecurityStateSyncAuthenticatesValidatorSetWithDashCore(t *testing.T) {
	honest, _ := types.RandValidatorSet(4)
	header := &types.Header{
		Height:            10,
		ValidatorsHash:    honest.Hash(),
		ProposerProTxHash: honest.Proposer().ProTxHash,
	}
	quorumInfo := &btcjson.QuorumInfoResult{
		Type:            honest.QuorumType.Name(),
		QuorumHash:      hex.EncodeToString(honest.QuorumHash),
		QuorumPublicKey: hex.EncodeToString(honest.ThresholdPublicKey.Bytes()),
	}
	for _, validator := range honest.Validators {
		quorumInfo.Members = append(quorumInfo.Members, btcjson.QuorumMember{
			ProTxHash:   hex.EncodeToString(validator.ProTxHash),
			PubKeyShare: hex.EncodeToString(validator.PubKey.Bytes()),
			Valid:       true,
		})
	}

	tests := []struct {
		name   string
		mutate func(*types.ValidatorSet)
	}{
		{"proposer", func(forged *types.ValidatorSet) {
			require.NoError(t, forged.SetProposer(forged.Validators[1].ProTxHash))
		}},
		{"voting_power_threshold", func(forged *types.ValidatorSet) {
			forged.VotingPowerThreshold = uint64(forged.TotalVotingPower())
		}},
		{"public_key_availability", func(forged *types.ValidatorSet) {
			forged.HasPublicKeys = false
		}},
		{"membership", func(forged *types.ValidatorSet) {
			replacement, _ := types.RandValidatorSet(len(forged.Validators))
			forged.Validators = replacement.Validators
			forged.HasPublicKeys = false
			require.NoError(t, forged.SetProposer(forged.Validators[0].ProTxHash))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forged := honest.Copy()
			test.mutate(forged)
			lightBlock := &types.LightBlock{
				SignedHeader: &types.SignedHeader{Header: header},
				ValidatorSet: forged,
			}

			authenticated, err := authenticateStateSyncValidatorSetWithQuorumInfo(lightBlock, quorumInfo)
			require.NoError(t, err)
			require.True(t, authenticated.HasPublicKeys)
			require.Zero(t, authenticated.VotingPowerThreshold)
			require.Equal(t, header.ProposerProTxHash, authenticated.Proposer().ProTxHash)
			require.Equal(t, honest.GetProTxHashesOrdered(), authenticated.GetProTxHashesOrdered())
			require.NoError(t, authenticated.ValidateBasic())
		})
	}

	t.Run("quorum_type", func(t *testing.T) {
		wrongInfo := *quorumInfo
		wrongType := btcjson.LLMQType_TEST
		if wrongType == honest.QuorumType {
			wrongType = btcjson.LLMQType_DEVNET
		}
		wrongInfo.Type = wrongType.Name()
		_, err := authenticateStateSyncValidatorSetWithQuorumInfo(
			&types.LightBlock{SignedHeader: &types.SignedHeader{Header: header}, ValidatorSet: honest},
			&wrongInfo,
		)
		require.Error(t, err)
	})
}
