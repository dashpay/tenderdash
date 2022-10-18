package state

import (
	"bytes"
	"fmt"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	tmtypes "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

const (
	InitChainSource       = "ResponseInitChain"
	PrepareProposalSource = "ResponsePrepareProposal"
	ProcessProposalSource = "ResponseProcessProposal"
)

// CurrentRoundState ...
type CurrentRoundState struct {
	// Base state for the changes
	Base State

	ProTxHash types.ProTxHash

	// AppHash of current block
	AppHash tmbytes.HexBytes `json:"app_hash"`

	// TxResults for current block
	TxResults []*abci.ExecTxResult `json:"tx_results"`
	// ResultsHash of current block
	ResultsHash []byte `json:"results_hash"`

	CoreChainLock *types.CoreChainLock

	// Items changed in next block

	NextConsensusParams              types.ConsensusParams
	LastHeightConsensusParamsChanged int64

	NextValidators              *types.ValidatorSet
	LastHeightValidatorsChanged int64

	Params RoundParams
}

// NewCurrentRoundState returns a new instance of CurrentRoundState
func NewCurrentRoundState(proTxHash types.ProTxHash, rp RoundParams, baseState State) (CurrentRoundState, error) {
	candidate := CurrentRoundState{
		Base:      baseState,
		ProTxHash: proTxHash,
		AppHash:   rp.AppHash.Copy(),
		Params:    rp,
	}
	err := candidate.populate()
	if err != nil {
		return CurrentRoundState{}, err
	}
	return candidate, nil
}

func (candidate *CurrentRoundState) IsEmpty() bool {
	return candidate.AppHash == nil
}

// UpdateBlock changes block fields to reflect the ones returned in PrepareProposalSource / ProcessProposalSource
func (candidate *CurrentRoundState) UpdateBlock(target *types.Block) error {
	if candidate.Params.Source != PrepareProposalSource {
		return fmt.Errorf("block can be updated only based on '%s' response, got '%s'", ProcessProposalSource, candidate.Params.Source)
	}
	target.AppHash = candidate.AppHash
	target.ResultsHash = candidate.ResultsHash
	target.NextValidatorsHash = candidate.NextValidators.Hash()
	target.CoreChainLock = candidate.CoreChainLock
	if candidate.CoreChainLock != nil {
		target.CoreChainLockedHeight = candidate.CoreChainLock.CoreBlockHeight
	} else {
		target.CoreChainLockedHeight = candidate.Base.LastCoreChainLockedBlockHeight
	}
	return nil
}

// UpdateState updates state when the block is committed. State will contain data needed by next block.
func (candidate *CurrentRoundState) UpdateState(target *State) error {
	target.AppHash = candidate.AppHash
	target.LastResultsHash = candidate.ResultsHash
	target.ConsensusParams = candidate.NextConsensusParams
	target.LastHeightConsensusParamsChanged = candidate.LastHeightConsensusParamsChanged
	target.Version.Consensus.App = candidate.NextConsensusParams.Version.AppVersion
	target.Validators = candidate.NextValidators
	target.LastHeightValidatorsChanged = candidate.LastHeightValidatorsChanged
	if candidate.CoreChainLock != nil {
		target.LastCoreChainLockedBlockHeight = candidate.CoreChainLock.CoreBlockHeight
	}
	return nil
}

// UpdateFunc implements UpdateFunc
func (candidate *CurrentRoundState) UpdateFunc(state State) (State, error) {
	err := candidate.UpdateState(&state)
	return state, err
}

func (candidate *CurrentRoundState) StateID() types.StateID {
	var appHash tmbytes.HexBytes
	if len(candidate.AppHash) > 0 {
		appHash = candidate.AppHash.Copy()
	} else {
		appHash = make([]byte, crypto.DefaultAppHashSize)
	}
	return types.StateID{
		Height:  candidate.GetHeight(),
		AppHash: appHash,
	}
}

// GetHeight returns height of current block
func (candidate *CurrentRoundState) GetHeight() int64 {
	if candidate.Base.LastBlockHeight == 0 {
		return candidate.Base.InitialHeight
	}

	return candidate.Base.LastBlockHeight + 1
}

func (candidate *CurrentRoundState) populate() error {
	populates := []func() error{
		candidate.populateTxResults,
		// Consensus params need to be populated before validators
		candidate.populateConsensusParams,
		candidate.populateValsetUpdates,
		candidate.populateChainlock,
	}
	for _, populate := range populates {
		err := populate()
		if err != nil {
			return err
		}
	}
	return nil
}

func (candidate *CurrentRoundState) populateTxResults() error {
	hash, err := abci.TxResultsHash(candidate.Params.TxResults)
	if err != nil {
		return fmt.Errorf("marshaling TxResults: %w", err)
	}
	candidate.ResultsHash = hash
	candidate.TxResults = candidate.Params.TxResults
	return nil
}

func (candidate *CurrentRoundState) populateChainlock() error {
	chainLock := candidate.Params.CoreChainLock

	lastChainLockHeight := candidate.Base.LastCoreChainLockedBlockHeight
	if chainLock == nil || (chainLock.CoreBlockHeight <= lastChainLockHeight) {
		candidate.CoreChainLock = nil
		return nil
	}
	candidate.CoreChainLock = chainLock
	return nil
}

// populateConsensusParams updates ConsensusParams, Version and LastHeightConsensusParamsChanged
func (candidate *CurrentRoundState) populateConsensusParams() error {
	updates := candidate.Params.ConsensusParamUpdates
	if updates == nil || updates.Equal(&tmtypes.ConsensusParams{}) {
		candidate.NextConsensusParams = candidate.Base.ConsensusParams
		candidate.LastHeightConsensusParamsChanged = candidate.Base.LastHeightConsensusParamsChanged
		return nil
	}

	current := candidate.NextConsensusParams
	if current.IsZero() {
		current = candidate.Base.ConsensusParams
	}

	// NOTE: must not mutate state.ConsensusParams
	nextParams := current.UpdateConsensusParams(updates)
	err := nextParams.ValidateConsensusParams()
	if err != nil {
		return fmt.Errorf("error updating consensus params: %w", err)
	}
	candidate.NextConsensusParams = nextParams

	// Change results from this height but only applies to the next height.
	candidate.LastHeightConsensusParamsChanged = candidate.GetHeight() + 1

	return nil
}

// populateValsetUpdates calculates and populates Validators and LastHeightValidatorsChanged
// CONTRACT: candidate.ConsensusParams were already populated
func (candidate *CurrentRoundState) populateValsetUpdates() error {
	update := candidate.Params.ValidatorSetUpdate
	updateSource := candidate.Params.Source

	base := candidate.Base

	newValSet, err := valsetUpdate(candidate.ProTxHash, update, base.Validators, candidate.NextConsensusParams.Validator)
	if err != nil {
		return fmt.Errorf("validator set updates: %w", err)
	}

	if updateSource != InitChainSource {
		// we take validator sets as they arrive from InitChainSource response
		newValSet.IncrementProposerPriority(1)
	}

	candidate.NextValidators = newValSet

	if updateSource != InitChainSource && update != nil && len(update.ValidatorUpdates) > 0 {
		candidate.LastHeightValidatorsChanged = candidate.GetHeight() + 1
	} else {
		candidate.LastHeightValidatorsChanged = base.LastHeightValidatorsChanged
	}

	return nil
}

// RoundParams contains parameters received from ABCI which are necessary for reaching a consensus
type RoundParams struct {
	AppHash               tmbytes.HexBytes
	TxResults             []*abci.ExecTxResult
	ConsensusParamUpdates *tmtypes.ConsensusParams
	ValidatorSetUpdate    *abci.ValidatorSetUpdate
	CoreChainLock         *types.CoreChainLock
	Source                string
}

// ToProcessProposal reconstructs ResponseProcessProposal structure from a current state of RoundParams
func (rp RoundParams) ToProcessProposal() *abci.ResponseProcessProposal {
	return &abci.ResponseProcessProposal{
		Status:                abci.ResponseProcessProposal_ACCEPT,
		AppHash:               rp.AppHash,
		TxResults:             rp.TxResults,
		ConsensusParamUpdates: rp.ConsensusParamUpdates,
		ValidatorSetUpdate:    rp.ValidatorSetUpdate,
	}
}

// RoundParamsFromPrepareProposal creates RoundParams from ResponsePrepareProposal
func RoundParamsFromPrepareProposal(resp *abci.ResponsePrepareProposal) (RoundParams, error) {
	rp := RoundParams{
		AppHash:               resp.AppHash,
		TxResults:             resp.TxResults,
		ConsensusParamUpdates: resp.ConsensusParamUpdates,
		ValidatorSetUpdate:    resp.ValidatorSetUpdate,
		Source:                PrepareProposalSource,
	}
	ccl, err := types.CoreChainLockFromProto(resp.CoreChainLockUpdate)
	if err != nil {
		return RoundParams{}, fmt.Errorf("core-chain-lock proto couldn't convert into domain entity: %w", err)
	}
	rp.CoreChainLock = ccl
	return rp, nil
}

// RoundParamsFromProcessProposal creates RoundParams from ResponseProcessProposal
func RoundParamsFromProcessProposal(resp *abci.ResponseProcessProposal, coreChainLock *types.CoreChainLock) RoundParams {
	rp := RoundParams{
		AppHash:               resp.AppHash,
		TxResults:             resp.TxResults,
		ConsensusParamUpdates: resp.ConsensusParamUpdates,
		ValidatorSetUpdate:    resp.ValidatorSetUpdate,
		Source:                ProcessProposalSource,
	}
	rp.CoreChainLock = coreChainLock
	return rp
}

// RoundParamsFromInitChain creates RoundParams from ResponseInitChain
func RoundParamsFromInitChain(resp *abci.ResponseInitChain) (RoundParams, error) {
	rp := RoundParams{
		AppHash:               resp.AppHash,
		ConsensusParamUpdates: resp.ConsensusParams,
		ValidatorSetUpdate:    &resp.ValidatorSetUpdate,
		Source:                InitChainSource,
	}
	ccl, err := types.CoreChainLockFromProto(resp.NextCoreChainLockUpdate)
	if err != nil {
		return RoundParams{}, err
	}
	rp.CoreChainLock = ccl
	return rp, nil
}

// valsetUpdate processes validator set updates received from ABCI app.
func valsetUpdate(
	nodeProTxHash types.ProTxHash,
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
			// if we don't have proTxHash, NewValidatorSetWithLocalNodeProTxHash behaves like NewValidatorSet
			nValSet = types.NewValidatorSetWithLocalNodeProTxHash(validatorUpdates, thresholdPubKey,
				currentVals.QuorumType, quorumHash, nodeProTxHash)
		}
	}
	return nValSet, nil
}
