package factory

import (
	"github.com/dashevo/dashd-go/btcjson"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/bytes"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

const ChainID = "test-chain"

// MinimalGenesisDoc generates a minimal working genesis doc.
// It is very similar to Dash Platform's production environment
// genesis doc, which assumes that all other settings (like validator
// set) will be provided by ABCI during initial handshake.
func MinimalGenesisDoc() types.GenesisDoc {
	genesisDoc := types.GenesisDoc{
		ChainID:    ChainID,
		QuorumType: btcjson.LLMQType_5_60,
		// ConsensusParams: &types.ConsensusParams{},
	}
	if err := genesisDoc.ValidateAndComplete(); err != nil {
		// should never happen
		panic("cannot generate minimal genesis doc: " + err.Error())
	}

	return genesisDoc
}

// RandGenesisDoc generates a genesis doc with random validator set.
// NOTE: It's better to use MinimalGensisDoc() which generates genesis doc
// similar to Dash Platform production environment.
func RandGenesisDoc(numValidators int, consensusParams *types.ConsensusParams) (*types.GenesisDoc, []types.PrivValidator) {
	genesisDoc := types.GenesisDoc{
		ChainID:    ChainID,
		QuorumType: btcjson.LLMQType_5_60,
		// ConsensusParams: &types.ConsensusParams{},
	}

	proTxHashes := crypto.RandProTxHashes(numValidators)
	valSetParams := types.NewValSetParam(proTxHashes)
	valSet, privValidators := types.GenerateValidatorSet(valSetParams)

	genesisVals := types.MakeGenesisValsFromValidatorSet(valSet)
	coreChainLock := types.NewMockChainLock(2)

	genesis := types.GenesisDoc{
		GenesisTime:     tmtime.Now(),
		InitialHeight:   1,
		ChainID:         ChainID,
		Validators:      genesisVals,
		ConsensusParams: consensusParams,
		AppHash:         make(bytes.HexBytes, crypto.DefaultAppHashSize),

		// dash fields
		InitialCoreChainLockedHeight: 1,
		InitialProposalCoreChainLock: coreChainLock.ToProto(),
		ThresholdPublicKey:           valSet.ThresholdPublicKey,
		QuorumHash:                   valSet.QuorumHash,
		QuorumType:                   btcjson.LLMQType_5_60,
	}
	if err := genesisDoc.ValidateAndComplete(); err != nil {
		// should never happen
		panic("cannot generate random genesis doc:" + err.Error())
	}

	return &genesis, privValidators
}
