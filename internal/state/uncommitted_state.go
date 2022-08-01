package state

import (
	"bytes"
	"context"
	"fmt"

	"github.com/gogo/protobuf/proto"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/dash"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	tmtypes "github.com/tendermint/tendermint/proto/tendermint/types"
	types2 "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

// UncommittedState ... ; TODO rename to CandidateState
type UncommittedState struct {
	// Base state for the changes
	Base    State
	AppHash []byte `json:"app_hash"`

	TxResults   []*abci.ExecTxResult `json:"tx_results"`
	ResultsHash []byte               `json:"last_results_hash"`

	ConsensusParams                  types.ConsensusParams
	LastHeightConsensusParamsChanged int64
	Version                          Version

	Validators                  *types.ValidatorSet
	LastHeightValidatorsChanged int64

	CoreChainLockedBlockHeight uint32 `json:"chain_locked_block_height"`
}

func NewUncommittedState(ctx context.Context, proposalResponse proto.Message, baseState State) (UncommittedState, error) {
	ret := UncommittedState{}
	err := ret.populate(ctx, proposalResponse, baseState)
	return ret, err
}

// UpdateState applies changes from the candidate state on a final state
func (candidate UncommittedState) UpdateState(ctx context.Context, target *State) error {
	target.AppHash = candidate.AppHash

	target.ConsensusParams = candidate.ConsensusParams
	target.LastHeightConsensusParamsChanged = candidate.LastHeightConsensusParamsChanged
	target.Version = candidate.Version

	target.Validators = candidate.Validators
	target.LastHeightValidatorsChanged = candidate.LastHeightValidatorsChanged

	target.LastCoreChainLockedBlockHeight = candidate.CoreChainLockedBlockHeight
	return nil
}

// UpdateFunc implements UpdateFunc
func (candidate UncommittedState) UpdateFunc(ctx context.Context, state State) (State, error) {
	err := candidate.UpdateState(ctx, &state)
	return state, err
}

func (candidate *UncommittedState) populate(ctx context.Context, proposalResponse proto.Message, baseState State) error {
	switch resp := proposalResponse.(type) {
	case *abci.ResponsePrepareProposal:
		return candidate.update(
			ctx,
			baseState,
			resp.AppHash,
			resp.TxResults,
			resp.ConsensusParamUpdates,
			resp.ValidatorSetUpdate,
			resp.NextCoreChainLockUpdate,
		)

	case *abci.ResponseProcessProposal:
		return candidate.update(
			ctx,
			baseState,
			resp.AppHash,
			resp.TxResults,
			resp.ConsensusParamUpdates,
			resp.ValidatorSetUpdate,
			resp.NextCoreChainLockUpdate,
		)
	case nil: // Assuming no changes
		return candidate.update(ctx, baseState, nil, nil, nil, nil, nil)

	default:
		return fmt.Errorf("unsupported response type %T", resp)
	}
}

func (candidate *UncommittedState) update(
	ctx context.Context,
	baseState State,
	appHash tmbytes.HexBytes,
	txResults []*abci.ExecTxResult,
	consensusParamUpdates *types2.ConsensusParams,
	validatorSetUpdate *abci.ValidatorSetUpdate,
	coreChainLockUpdate *types2.CoreChainLock,
) error {
	candidate.Base = baseState
	candidate.AppHash = appHash

	if err := candidate.populateTxResults(txResults); err != nil {
		return err
	}
	// Consensus params need to be populated before validators
	if err := candidate.populateConsensusParams(consensusParamUpdates); err != nil {
		return err
	}
	if err := candidate.populateValsetUpdates(ctx, validatorSetUpdate); err != nil {
		return err
	}
	if err := candidate.populateChainlock(coreChainLockUpdate); err != nil {
		return err
	}

	return nil
}

// Height returns height of current block
func (candidate *UncommittedState) Height() int64 {
	return candidate.Base.LastBlockHeight + 1
}

func (candidate *UncommittedState) populateTxResults(txResults []*abci.ExecTxResult) error {
	hash, err := abci.TxResultsHash(txResults)
	if err != nil {
		return fmt.Errorf("marshaling TxResults: %w", err)
	}
	candidate.ResultsHash = hash
	candidate.TxResults = txResults

	return nil
}

func (candidate *UncommittedState) populateChainlock(chainlock *types2.CoreChainLock) error {
	nextCoreChainLock, err := types.CoreChainLockFromProto(chainlock)
	if err != nil {
		return err
	}
	if nextCoreChainLock == nil || (nextCoreChainLock.CoreBlockHeight <= candidate.CoreChainLockedBlockHeight) {
		candidate.CoreChainLockedBlockHeight = candidate.Base.LastCoreChainLockedBlockHeight
		return nil
	}

	candidate.CoreChainLockedBlockHeight = nextCoreChainLock.CoreBlockHeight
	return nil
}

// populateConsensusParams updates ConsensusParams, Version and LastHeightConsensusParamsChanged
func (candidate *UncommittedState) populateConsensusParams(updates *tmtypes.ConsensusParams) error {
	base := candidate.Base

	if updates == nil {
		candidate.ConsensusParams = base.ConsensusParams
		candidate.Version = base.Version
		candidate.LastHeightConsensusParamsChanged = base.LastHeightConsensusParamsChanged

		return nil
	}

	// NOTE: must not mutate state.ConsensusParams
	nextParams := candidate.ConsensusParams.UpdateConsensusParams(updates)
	err := nextParams.ValidateConsensusParams()
	if err != nil {
		return fmt.Errorf("error updating consensus params: %w", err)
	}

	candidate.ConsensusParams = nextParams
	candidate.Version.Consensus.App = nextParams.Version.AppVersion

	// Change results from this height but only applies to the next height.
	candidate.LastHeightConsensusParamsChanged = candidate.Height() + 1

	return nil
}

// populateValsetUpdates calculates and populates Validators and LastHeightValidatorsChanged
// CONTRACT: candidate.ConsensusParams were already populated
func (candidate *UncommittedState) populateValsetUpdates(ctx context.Context, update *abci.ValidatorSetUpdate) error {
	base := candidate.Base

	newValSet, err := valsetUpdate(ctx, update, base.Validators, candidate.ConsensusParams.Validator)
	if err != nil {
		return fmt.Errorf("validator set updates: %w", err)
	}
	newValSet.IncrementProposerPriority(1)
	candidate.Validators = newValSet

	if update != nil && len(update.ValidatorUpdates) > 0 {
		// Change results from this height but only applies to the next height.
		candidate.LastHeightValidatorsChanged = candidate.Height() + 1
	} else {
		candidate.LastHeightValidatorsChanged = base.LastHeightValidatorsChanged
	}

	return nil
}

// valsetUpdate processes validator set updates received from ABCI app.
func valsetUpdate(
	ctx context.Context,
	vu *abci.ValidatorSetUpdate,
	currentVals *types.ValidatorSet,
	params types.ValidatorParams,
) (*types.ValidatorSet, error) {
	err := validateValidatorSetUpdate(vu, params)
	if err != nil {

		return nil, fmt.Errorf("validating validator updates: %w", err)
	}

	validatorUpdates, thresholdPubKey, quorumHash, err := types.PB2TM.ValidatorUpdatesFromValidatorSet(vu)
	if err != nil {
		return nil, fmt.Errorf("converting validator updates to native types: %w", err)
	}
	// Copy the valset so we can apply changes from FinalizeBlock
	// and update s.LastValidators and s.Validators.
	nValSet := currentVals.Copy()
	// Update the validator set with the latest abciResponses.
	if len(validatorUpdates) > 0 {
		if bytes.Equal(nValSet.QuorumHash, quorumHash) {
			err = nValSet.UpdateWithChangeSet(validatorUpdates, thresholdPubKey, quorumHash)
			if err != nil {
				return nil, err
			}
		} else {
			nodeProTxHash, _ := dash.ProTxHashFromContext(ctx)
			// if we don't have proTxHash, NewValidatorSetWithLocalNodeProTxHash behaves like NewValidatorSet
			nValSet = types.NewValidatorSetWithLocalNodeProTxHash(validatorUpdates, thresholdPubKey,
				currentVals.QuorumType, quorumHash, nodeProTxHash)
		}
	}
	return nValSet, nil
}
