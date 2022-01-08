package factory

import (
	"context"
	"fmt"
	"sort"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/types"
)

func GenerateGenesisValidators(
	numValidators int,
) ([]types.GenesisValidator, []types.PrivValidator, crypto.QuorumHash, crypto.PubKey) {
	var (
		genesisValidators = make([]types.GenesisValidator, numValidators)
		privValidators    = make([]types.PrivValidator, numValidators)
	)
	privateKeys, proTxHashes, thresholdPublicKey :=
		bls12381.CreatePrivLLMQDataDefaultThreshold(numValidators)
	quorumHash := crypto.RandQuorumHash()

	for i := 0; i < numValidators; i++ {
		privValidators[i] = types.NewMockPVWithParams(privateKeys[i], proTxHashes[i], quorumHash,
			thresholdPublicKey, false, false)
		genesisValidators[i] = types.GenesisValidator{
			PubKey:    privateKeys[i].PubKey(),
			Power:     types.DefaultDashVotingPower,
			ProTxHash: proTxHashes[i],
		}
	}

	sort.Sort(types.PrivValidatorsByProTxHash(privValidators))
	sort.Sort(types.GenesisValidatorsByProTxHash(genesisValidators))

	return genesisValidators, privValidators, quorumHash, thresholdPublicKey
}

func GenerateMockGenesisValidators(
	numValidators int,
) ([]types.GenesisValidator, []*types.MockPV, crypto.QuorumHash, crypto.PubKey) {
	var (
		genesisValidators = make([]types.GenesisValidator, numValidators)
		privValidators    = make([]*types.MockPV, numValidators)
	)
	privateKeys, proTxHashes, thresholdPublicKey :=
		bls12381.CreatePrivLLMQDataDefaultThreshold(numValidators)
	quorumHash := crypto.RandQuorumHash()

	for i := 0; i < numValidators; i++ {
		privValidators[i] = types.NewMockPVWithParams(
			privateKeys[i],
			proTxHashes[i],
			quorumHash,
			thresholdPublicKey,
			false,
			false,
		)
		genesisValidators[i] = types.GenesisValidator{
			PubKey:    privateKeys[i].PubKey(),
			Power:     types.DefaultDashVotingPower,
			ProTxHash: proTxHashes[i],
		}
	}

	sort.Sort(types.MockPrivValidatorsByProTxHash(privValidators))
	sort.Sort(types.GenesisValidatorsByProTxHash(genesisValidators))

	return genesisValidators, privValidators, quorumHash, thresholdPublicKey
}

func GenerateMockValidatorSetUpdatingPrivateValidatorsAtHeight(
	proTxHashes []crypto.ProTxHash,
	mockPVs map[string]*types.MockPV,
	height int64,
) (*types.ValidatorSet, []*types.MockPV) {
	numValidators := len(mockPVs)
	if numValidators < 2 {
		panic("there should be at least 2 validators")
	}
	var (
		valz           = make([]*types.Validator, numValidators)
		privValidators = make([]*types.MockPV, numValidators)
	)
	orderedProTxHashes, privateKeys, thresholdPublicKey :=
		bls12381.CreatePrivLLMQDataOnProTxHashesDefaultThreshold(proTxHashes)
	quorumHash := crypto.RandQuorumHash()

	for i := 0; i < numValidators; i++ {
		if mockPV, ok := mockPVs[orderedProTxHashes[i].String()]; ok {
			mockPV.UpdatePrivateKey(context.Background(), privateKeys[i], quorumHash, thresholdPublicKey, height)
			privValidators[i] = mockPV
		} else {
			privValidators[i] = types.NewMockPVWithParams(privateKeys[i], orderedProTxHashes[i], quorumHash,
				thresholdPublicKey, false, false)
		}
		valz[i] = types.NewValidatorDefaultVotingPower(privateKeys[i].PubKey(), orderedProTxHashes[i])
	}

	sort.Sort(types.MockPrivValidatorsByProTxHash(privValidators))

	return types.NewValidatorSet(valz, thresholdPublicKey, crypto.SmallQuorumType(), quorumHash, true), privValidators
}

func RandValidator() (*types.Validator, types.PrivValidator) {
	quorumHash := crypto.RandQuorumHash()
	privVal := types.NewMockPVForQuorum(quorumHash)
	proTxHash, err := privVal.GetProTxHash(context.Background())
	if err != nil {
		panic(fmt.Errorf("could not retrieve proTxHash %w", err))
	}
	pubKey, err := privVal.GetPubKey(context.Background(), quorumHash)
	if err != nil {
		panic(fmt.Errorf("could not retrieve pubkey %w", err))
	}
	val := types.NewValidatorDefaultVotingPower(pubKey, proTxHash)
	return val, privVal
}
