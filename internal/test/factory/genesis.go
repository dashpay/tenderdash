package factory

import (
	"sort"

	"github.com/dashevo/dashd-go/btcjson"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/dash/llmq"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

func TestGenesisDoc(valSet *types.ValidatorSet, appHash tmbytes.HexBytes) types.GenesisDoc {
	genVals := types.MakeGenesisValsFromValidatorSet(valSet)
	return types.GenesisDoc{
		GenesisTime:        tmtime.Now(),
		ChainID:            "test-chain",
		Validators:         genVals,
		ConsensusParams:    types.DefaultConsensusParams(),
		ThresholdPublicKey: valSet.ThresholdPublicKey,
		QuorumHash:         valSet.QuorumHash,
		QuorumType:         btcjson.LLMQType_5_60,
		AppHash:            nil,
	}
}

func RandGenesisDoc(
	cfg *config.Config,
	numValidators int,
	initialHeight int64,
	consensusParams *types.ConsensusParams,
) (*types.GenesisDoc, []types.PrivValidator) {
	validators := make([]types.GenesisValidator, 0, numValidators)
	privValidators := make([]types.PrivValidator, 0, numValidators)

	ld := llmq.MustGenerate(crypto.RandProTxHashes(numValidators))
	quorumHash := crypto.RandQuorumHash()
	iter := ld.Iter()
	for iter.Next() {
		proTxHash, qks := iter.Value()
		validators = append(validators, types.GenesisValidator{
			PubKey:    qks.PubKey,
			Power:     types.DefaultDashVotingPower,
			ProTxHash: proTxHash,
		})
		privValidators = append(privValidators, types.NewMockPVWithParams(
			qks.PrivKey,
			proTxHash,
			quorumHash,
			ld.ThresholdPubKey,
			false,
			false,
		))
	}
	sort.Sort(types.PrivValidatorsByProTxHash(privValidators))

	coreChainLock := types.NewMockChainLock(2)

	return &types.GenesisDoc{
		GenesisTime:     tmtime.Now(),
		InitialHeight:   initialHeight,
		ChainID:         cfg.ChainID(),
		Validators:      validators,
		ConsensusParams: consensusParams,
		AppHash:         make([]byte, crypto.DefaultAppHashSize),

		// dash fields
		InitialCoreChainLockedHeight: 1,
		InitialProposalCoreChainLock: coreChainLock.ToProto(),
		ThresholdPublicKey:           ld.ThresholdPubKey,
		QuorumHash:                   quorumHash,
		QuorumType:                   btcjson.LLMQType_5_60,
	}, privValidators
}
