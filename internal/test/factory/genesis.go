package factory

import (
	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/bytes"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// MinimalGenesisDoc generates a minimal working genesis doc.
// It is very similar to Dash Platform's production environment
// genesis doc, which assumes that all other settings (like validator
// set) will be provided by ABCI during initial handshake.
func MinimalGenesisDoc() types.GenesisDoc {
	genesisDoc := types.GenesisDoc{
		ChainID:    DefaultTestChainID,
		QuorumType: btcjson.LLMQType_5_60,
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
	proTxHashes := crypto.RandProTxHashes(numValidators)
	valSetParams := types.NewValSetParam(proTxHashes)
	valSet, privValidators := types.GenerateValidatorSet(valSetParams)

	genesisVals := types.MakeGenesisValsFromValidatorSet(valSet)
	coreChainLock := types.NewMockChainLock(2)

	genesisDoc := types.GenesisDoc{
		GenesisTime:     tmtime.Now(),
		InitialHeight:   1,
		ChainID:         DefaultTestChainID,
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

	return &genesisDoc, privValidators
}
