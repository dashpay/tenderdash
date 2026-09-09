package statesync

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/types"
)

// A state-sync provider must not be able to install validator membership or
// live consensus controls that the legacy ValidatorsHash does not authenticate.
func TestStateSyncAuthenticatesValidatorSetWithDashCore(t *testing.T) {
	honest, _ := types.RandValidatorSet(4)
	header := &types.Header{
		Height:            10,
		ValidatorsHash:    honest.Hash(),
		ProposerProTxHash: honest.Proposer().ProTxHash,
	}
	quorumInfo := quorumInfoFromValidatorSet(honest, true)

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

func TestStateSyncDoesNotTrustWireValidatorAddresses(t *testing.T) {
	honest, _ := types.RandValidatorSet(4)
	for _, validator := range honest.Validators {
		validator.NodeAddress = types.ValidatorAddress{
			NodeID:   types.NodeID("0102030405060708090a0b0c0d0e0f1011121314"),
			Hostname: "attacker.example",
			Port:     26656,
		}
	}
	header := &types.Header{
		Height:            10,
		ValidatorsHash:    honest.Hash(),
		ProposerProTxHash: honest.Proposer().ProTxHash,
	}
	quorumInfo := quorumInfoFromValidatorSet(honest, true)

	authenticated, err := authenticateStateSyncValidatorSetWithQuorumInfo(
		&types.LightBlock{SignedHeader: &types.SignedHeader{Header: header}, ValidatorSet: honest},
		quorumInfo,
	)
	require.NoError(t, err)
	for _, validator := range authenticated.Validators {
		require.Zero(t, validator.NodeAddress)
	}
}

func TestStateSyncAcceptsMissingCorePublicKeyShares(t *testing.T) {
	honest, _ := types.RandValidatorSet(4)
	header := &types.Header{
		Height:            10,
		ValidatorsHash:    honest.Hash(),
		ProposerProTxHash: honest.Proposer().ProTxHash,
	}
	quorumInfo := quorumInfoFromValidatorSet(honest, false)

	authenticated, err := authenticateStateSyncValidatorSetWithQuorumInfo(
		&types.LightBlock{SignedHeader: &types.SignedHeader{Header: header}, ValidatorSet: honest},
		quorumInfo,
	)
	require.NoError(t, err)
	require.False(t, authenticated.HasPublicKeys)
	for _, validator := range authenticated.Validators {
		require.Nil(t, validator.PubKey)
	}
	require.Equal(t, honest.GetProTxHashesOrdered(), authenticated.GetProTxHashesOrdered())
	require.NoError(t, authenticated.ValidateBasic())
}

func quorumInfoFromValidatorSet(valSet *types.ValidatorSet, includeShares bool) *btcjson.QuorumInfoResult {
	info := &btcjson.QuorumInfoResult{
		Type:            valSet.QuorumType.Name(),
		QuorumHash:      hex.EncodeToString(valSet.QuorumHash),
		QuorumPublicKey: hex.EncodeToString(valSet.ThresholdPublicKey.Bytes()),
	}
	for _, validator := range valSet.Validators {
		member := btcjson.QuorumMember{
			ProTxHash: hex.EncodeToString(validator.ProTxHash),
			Valid:     true,
		}
		if includeShares {
			member.PubKeyShare = hex.EncodeToString(validator.PubKey.Bytes())
		}
		info.Members = append(info.Members, member)
	}
	return info
}

type quorumInfoTestClient struct {
	dashcore.Client
	info  *btcjson.QuorumInfoResult
	err   error
	calls int
}

func (c *quorumInfoTestClient) QuorumInfo(btcjson.LLMQType, crypto.QuorumHash) (*btcjson.QuorumInfoResult, error) {
	c.calls++
	return c.info, c.err
}

func TestValidatorSetAuthenticationCachesOnlyValidatedQuorumInfo(t *testing.T) {
	vals, _ := types.RandValidatorSet(4)
	block := &types.LightBlock{SignedHeader: &types.SignedHeader{Header: &types.Header{
		ValidatorsHash: vals.Hash(), ProposerProTxHash: vals.Proposer().ProTxHash,
	}}, ValidatorSet: vals}
	core := &quorumInfoTestClient{err: errors.New("unavailable")}
	auth := stateSyncValidatorSetAuthenticator{client: core}
	_, err := auth.authenticate(block)
	require.ErrorContains(t, err, "unavailable")
	core.err = nil
	core.info = quorumInfoFromValidatorSet(vals, true)
	_, err = auth.authenticate(block)
	require.NoError(t, err)
	block.ProposerProTxHash = vals.Validators[1].ProTxHash
	authenticated, err := auth.authenticate(block)
	require.NoError(t, err)
	require.Equal(t, block.ProposerProTxHash, authenticated.Proposer().ProTxHash)
	require.Equal(t, 2, core.calls)
	other, _ := types.RandValidatorSet(4)
	block.ValidatorSet = other
	block.ValidatorsHash = other.Hash()
	block.ProposerProTxHash = other.Proposer().ProTxHash
	core.info = quorumInfoFromValidatorSet(other, true)
	_, err = auth.authenticate(block)
	require.NoError(t, err)
	require.Equal(t, 3, core.calls)
}
