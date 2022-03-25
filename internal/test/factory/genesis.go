package factory

import (
	"sort"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/dash/llmq"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

func RandGenesisDoc(
	cfg *config.Config,
	numValidators int,
	initialHeight int64,
) (*types.GenesisDoc, []types.PrivValidator) {
	validators := make([]types.GenesisValidator, numValidators)
	privValidators := make([]types.PrivValidator, numValidators)

	ld := llmq.MustGenerate(crypto.RandProTxHashes(numValidators))
	quorumHash := crypto.RandQuorumHash()
	for i := 0; i < numValidators; i++ {
		val := types.NewValidatorDefaultVotingPower(ld.PubKeyShares[i], ld.ProTxHashes[i])
		validators[i] = types.GenesisValidator{
			PubKey:    val.PubKey,
			Power:     val.VotingPower,
			ProTxHash: val.ProTxHash,
		}
		privValidators[i] = types.NewMockPVWithParams(
			ld.PrivKeyShares[i],
			ld.ProTxHashes[i],
			quorumHash,
			ld.ThresholdPubKey,
			false,
			false,
		)
	}
	sort.Sort(types.PrivValidatorsByProTxHash(privValidators))

	coreChainLock := types.NewMockChainLock(2)

	return &types.GenesisDoc{
		GenesisTime:                  tmtime.Now(),
		InitialHeight:                initialHeight,
		ChainID:                      cfg.ChainID(),
		Validators:                   validators,
		InitialCoreChainLockedHeight: 1,
		InitialProposalCoreChainLock: coreChainLock.ToProto(),
		ThresholdPublicKey:           ld.ThresholdPubKey,
		QuorumHash:                   quorumHash,
	}, privValidators
}
