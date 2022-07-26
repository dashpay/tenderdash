package types

import (
	abci "github.com/tendermint/tendermint/abci/types"
	types2 "github.com/tendermint/tendermint/proto/tendermint/types"
)

// UncommittedState ...
type UncommittedState struct {
	AppHash               []byte                   `json:"app_hash"`
	LastResultsHash       []byte                   `json:"last_results_hash"`
	ValidatorSetUpdate    *abci.ValidatorSetUpdate `json:"validator_set_update"`
	ConsensusParamUpdates *types2.ConsensusParams  `json:"consensus_param_updates"`
	TxResults             []*abci.ExecTxResult     `json:"tx_results"`

	CoreChainLockedBlockHeight uint32 `json:"chain_locked_block_height"`
}
