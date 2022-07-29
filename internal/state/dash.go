package state

import (
	"context"
	"fmt"

	abci "github.com/tendermint/tendermint/abci/types"
	ctypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmtypes "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

// UpdateFunc is a function that can be used to update state
type UpdateFunc func(State) (State, error)

// PrepareStateUpdates generates state updates that will set Dash-related state fields.
func PrepareStateUpdates(
	ctx context.Context,
	changes ctypes.UncommittedState,
	blockHeader types.Header,
	state State,
) ([]UpdateFunc, error) {

	err := validateValidatorSetUpdate(changes.ValidatorSetUpdate, state.ConsensusParams.Validator)
	if err != nil {
		return nil, err
	}
	updates := []UpdateFunc{
		updateAppHash(changes.AppHash),
		updateResultHash(changes.TxResults),
		updateStateConsensusParams(blockHeader.Height, changes.ConsensusParamUpdates),
		updateStateNextValidators(
			ctx,
			blockHeader.Height,
			changes.ValidatorSetUpdate,
			state.ConsensusParams.Validator,
		),
	}
	return updates, nil
}

func executeStateUpdates(state State, updates ...UpdateFunc) (State, error) {
	var err error
	for _, update := range updates {
		state, err = update(state)
		if err != nil {
			return State{}, err
		}
	}
	return state, nil
}

func updateResultHash(txResults []*abci.ExecTxResult) UpdateFunc {
	return func(state State) (State, error) {
		hash, err := abci.TxResultsHash(txResults)
		if err != nil {
			return state, fmt.Errorf("marshaling TxResults: %w", err)
		}

		state.LastResultsHash = hash
		return state, nil
	}
}

func updateAppHash(appHash []byte) func(State) (State, error) {
	return func(state State) (State, error) {
		state.AppHash = appHash
		return state, nil
	}
}

func updateStateConsensusParams(
	height int64,
	consensusParamUpdates *tmtypes.ConsensusParams,
) UpdateFunc {
	return func(state State) (State, error) {
		// Update the params with the latest abciResponses.
		nextParams := state.ConsensusParams
		if consensusParamUpdates != nil {
			// NOTE: must not mutate state.ConsensusParams
			nextParams = state.ConsensusParams.UpdateConsensusParams(consensusParamUpdates)
			err := nextParams.ValidateConsensusParams()
			if err != nil {
				return state, fmt.Errorf("error updating consensus params: %w", err)
			}

			state.Version.Consensus.App = nextParams.Version.AppVersion

			// Change results from this height but only applies to the next height.
			state.LastHeightConsensusParamsChanged = height + 1
		}
		state.ConsensusParams = nextParams
		return state, nil
	}
}

func updateStateNextValidators(
	ctx context.Context,
	height int64,
	validatorSetUpdate *abci.ValidatorSetUpdate,
	params types.ValidatorParams,
) UpdateFunc {
	return func(state State) (State, error) {
		nValSet, err := valsetUpdate(ctx, validatorSetUpdate, state.Validators, state.ConsensusParams.Validator)
		if err != nil {
			return state, fmt.Errorf("error in validator updates: %w", err)
		}
		if validatorSetUpdate != nil && len(validatorSetUpdate.ValidatorUpdates) > 0 {
			// Change results from this height but only applies to the next height.
			state.LastHeightValidatorsChanged = height + 1
		}
		// Update validator proposer priority and set state variables.
		nValSet.IncrementProposerPriority(1)
		state.Validators = nValSet
		return state, nil
	}
}
