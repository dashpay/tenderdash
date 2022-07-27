package types

import (
	"fmt"

	"github.com/gogo/protobuf/proto"

	abci "github.com/tendermint/tendermint/abci/types"
	types2 "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

// UncommittedState ...
type UncommittedState struct {
	AppHash               []byte                   `json:"app_hash"`
	ResultsHash           []byte                   `json:"last_results_hash"`
	ValidatorSetUpdate    *abci.ValidatorSetUpdate `json:"validator_set_update"`
	ConsensusParamUpdates *types2.ConsensusParams  `json:"consensus_param_updates"`
	TxResults             []*abci.ExecTxResult     `json:"tx_results"`

	CoreChainLockedBlockHeight uint32 `json:"chain_locked_block_height"`
}

func (uncommittedState *UncommittedState) Populate(proposalResponse proto.Message) error {
	switch resp := proposalResponse.(type) {
	case *abci.ResponsePrepareProposal:
		uncommittedState.AppHash = resp.AppHash
		uncommittedState.ValidatorSetUpdate = resp.ValidatorSetUpdate
		uncommittedState.ConsensusParamUpdates = resp.ConsensusParamUpdates

		uncommittedState.populateChainlock(resp.NextCoreChainLockUpdate)
		uncommittedState.populateTxResults(resp.TxResults)

	case *abci.ResponseProcessProposal:
		uncommittedState.AppHash = resp.AppHash
		uncommittedState.ValidatorSetUpdate = resp.ValidatorSetUpdate
		uncommittedState.ConsensusParamUpdates = resp.ConsensusParamUpdates

		uncommittedState.populateChainlock(resp.NextCoreChainLockUpdate)
		uncommittedState.populateTxResults(resp.TxResults)

	default:
		return fmt.Errorf("unsupported response type %T", resp)
	}

	// update some round state data
	return nil
}

func (uncommittedState *UncommittedState) populateTxResults(txResults []*abci.ExecTxResult) error {
	hash, err := abci.TxResultsHash(txResults)
	if err != nil {
		return fmt.Errorf("marshaling TxResults: %w", err)
	}
	uncommittedState.ResultsHash = hash
	uncommittedState.TxResults = txResults

	return nil
}

func (uncommittedState *UncommittedState) populateChainlock(chainlock *types2.CoreChainLock) error {
	nextCoreChainLock, err := types.CoreChainLockFromProto(chainlock)
	if err != nil {
		return err
	}
	if nextCoreChainLock != nil && nextCoreChainLock.CoreBlockHeight <= uncommittedState.CoreChainLockedBlockHeight {
		return nil // noop
	}
	uncommittedState.CoreChainLockedBlockHeight = nextCoreChainLock.CoreBlockHeight

	return nil
}
