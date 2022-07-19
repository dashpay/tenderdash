package state

import (
	"bytes"
	"fmt"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/merkle"
	tmtypes "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

func prepareStateUpdates(
	nodeProTxHash crypto.ProTxHash,
	lastHeight int64,
	resp interface{},
	state State,
) ([]func(State) (State, error), error) {
	var (
		appHash               []byte
		validatorSetUpdate    *abci.ValidatorSetUpdate
		consensusParamUpdates *tmtypes.ConsensusParams
		txResults             []*abci.ExecTxResult
	)
	switch t := resp.(type) {
	case *abci.ResponsePrepareProposal:
		appHash = t.AppHash
		validatorSetUpdate = t.ValidatorSetUpdate
		consensusParamUpdates = t.ConsensusParamUpdates
		txResults = t.TxResults
	case *abci.ResponseProcessProposal:
		appHash = t.AppHash
		validatorSetUpdate = t.ValidatorSetUpdate
		consensusParamUpdates = t.ConsensusParamUpdates
		txResults = t.TxResults
	case *abci.ResponseFinalizeBlock:
		appHash = t.AppHash
		validatorSetUpdate = t.ValidatorSetUpdate
		consensusParamUpdates = t.ConsensusParamUpdates
		txResults = t.TxResults
	}
	err := validateValidatorSetUpdate(validatorSetUpdate, state.ConsensusParams.Validator)
	if err != nil {
		return nil, err
	}
	updates := []func(State) (State, error){
		updateAppHash(appHash),
		updateResultHash(txResults),
		updateStatConsensusParams(lastHeight, consensusParamUpdates),
		updateStateValidator(
			nodeProTxHash,
			lastHeight,
			validatorSetUpdate,
			state.ConsensusParams.Validator,
		),
	}
	return updates, nil
}

func executeStateUpdates(state State, updates ...func(State) (State, error)) (State, error) {
	var err error
	for _, update := range updates {
		state, err = update(state)
		if err != nil {
			return State{}, err
		}
	}
	return state, nil
}

func updateResultHash(txResults []*abci.ExecTxResult) func(State) (State, error) {
	return func(state State) (State, error) {
		// Update the state with the block and responses.
		rs, err := abci.MarshalTxResults(txResults)
		if err != nil {
			return state, fmt.Errorf("marshaling TxResults: %w", err)
		}
		state.LastResultsHash = merkle.HashFromByteSlices(rs)
		return state, nil
	}
}

func updateAppHash(appHash []byte) func(State) (State, error) {
	return func(state State) (State, error) {
		state.AppHash = appHash
		return state, nil
	}
}

func updateStatConsensusParams(
	lastHeight int64,
	consensusParamUpdates *tmtypes.ConsensusParams,
) func(State) (State, error) {
	return func(state State) (State, error) {
		// Update the params with the latest abciResponses.
		nextParams := state.ConsensusParams
		lastHeightParamsChanged := state.LastHeightConsensusParamsChanged
		if consensusParamUpdates != nil {
			// NOTE: must not mutate state.ConsensusParams
			nextParams = state.ConsensusParams.UpdateConsensusParams(consensusParamUpdates)
			err := nextParams.ValidateConsensusParams()
			if err != nil {
				return state, fmt.Errorf("error updating consensus params: %w", err)
			}

			state.Version.Consensus.App = nextParams.Version.AppVersion

			// Change results from this height but only applies to the next height.
			lastHeightParamsChanged = lastHeight + 1
		}
		state.LastHeightValidatorsChanged = lastHeightParamsChanged
		state.ConsensusParams = nextParams
		return state, nil
	}
}

func updateStateValidator(
	nodeProTxHash crypto.ProTxHash,
	lastHeight int64,
	validatorSetUpdate *abci.ValidatorSetUpdate,
	params types.ValidatorParams,
) func(state State) (State, error) {
	return func(state State) (State, error) {
		err := validateValidatorSetUpdate(validatorSetUpdate, params)
		if err != nil {
			return state, fmt.Errorf("error in validator updates: %w", err)
		}
		// The quorum type should not even matter here
		validatorUpdates, thresholdPubKey, quorumHash, err := types.PB2TM.ValidatorUpdatesFromValidatorSet(validatorSetUpdate)
		if err != nil {
			return state, fmt.Errorf("error in chain lock from proto: %v", err)
		}
		// Copy the valset so we can apply changes from FinalizeBlock
		// and update s.LastValidators and s.Validators.
		nValSet := state.Validators.Copy()
		// Update the validator set with the latest abciResponses.
		lastHeightValsChanged := state.LastHeightValidatorsChanged
		if len(validatorUpdates) > 0 {
			if bytes.Equal(nValSet.QuorumHash, quorumHash) {
				err := nValSet.UpdateWithChangeSet(validatorUpdates, thresholdPubKey, quorumHash)
				if err != nil {
					return state, fmt.Errorf("error changing validator set: %w", err)
				}
			} else {
				nValSet = types.NewValidatorSetWithLocalNodeProTxHash(validatorUpdates, thresholdPubKey,
					state.Validators.QuorumType, quorumHash, nodeProTxHash)
			}
			// Change results from this height but only applies to the next height.
			lastHeightValsChanged = lastHeight + 1 // +1
		}
		// Update validator proposer priority and set state variables.
		nValSet.IncrementProposerPriority(1)
		state.LastHeightValidatorsChanged = lastHeightValsChanged
		state.NextValidators = nValSet
		return state, nil
	}
}
