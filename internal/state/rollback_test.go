package state_test

import (
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

func TestRollback(t *testing.T) {
	var (
		height     int64 = 100
		nextHeight int64 = 101
	)

	blockStore := &mocks.BlockStore{}
	stateStore := setupStateStore(t, height)
	initialState, err := stateStore.Load()
	require.NoError(t, err)

	// perform the rollback over a version bump
	newParams := types.DefaultConsensusParams()
	newParams.Version.AppVersion = 11
	newParams.Block.MaxBytes = 1000
	nextState := initialState.Copy()
	nextState.LastBlockHeight = nextHeight
	nextState.Version.Consensus.App = 11
	nextState.LastBlockID = factory.MakeBlockID()
	nextState.LastAppHash = factory.RandomHash()
	nextState.LastValidators = initialState.Validators
	nextState.ConsensusParams = *newParams
	nextState.LastHeightConsensusParamsChanged = nextHeight + 1
	nextState.LastHeightValidatorsChanged = nextHeight + 1

	// update the state
	require.NoError(t, stateStore.Save(nextState))

	block := &types.BlockMeta{
		BlockID: initialState.LastBlockID,
		Header: types.Header{
			Height:             initialState.LastBlockHeight,
			AppHash:            factory.RandomHash(),
			LastBlockID:        factory.MakeBlockID(),
			ResultsHash:        initialState.LastResultsHash,
			ProposerProTxHash:  initialState.Validators.Validators[0].ProTxHash,
			ValidatorsHash:     initialState.Validators.Hash(),
			NextValidatorsHash: nextState.Validators.Hash(),
		},
	}
	nextBlock := &types.BlockMeta{
		BlockID: initialState.LastBlockID,
		Header: types.Header{
			Height:            nextState.LastBlockHeight,
			AppHash:           initialState.LastAppHash,
			LastBlockID:       block.BlockID,
			ResultsHash:       nextState.LastResultsHash,
			ProposerProTxHash: nextState.Validators.Validators[1].ProTxHash,
			ValidatorsHash:    nextState.Validators.Hash(),
		},
	}
	blockStore.On("LoadBlockMeta", height).Return(block)
	blockStore.On("LoadBlockMeta", nextHeight).Return(nextBlock)
	blockStore.On("Height").Return(nextHeight)
	blockStore.On("Base").Return(height)

	// rollback the state
	rollbackHeight, rollbackHash, err := state.Rollback(blockStore, stateStore)
	require.NoError(t, err)
	require.EqualValues(t, height, rollbackHeight)
	require.EqualValues(t, initialState.LastAppHash, rollbackHash)
	blockStore.AssertExpectations(t)

	// assert that we've recovered the prior state
	loadedState, err := stateStore.Load()
	require.NoError(t, err)
	require.EqualValues(t, initialState, loadedState)
}

func TestRollbackNoState(t *testing.T) {
	stateStore := state.NewStore(dbm.NewMemDB())
	blockStore := &mocks.BlockStore{}

	_, _, err := state.Rollback(blockStore, stateStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no state found")
}

func TestRollbackNoBlocks(t *testing.T) {
	const height = int64(100)

	stateStore := setupStateStore(t, height)
	blockStore := &mocks.BlockStore{}
	blockStore.On("Height").Return(height)
	blockStore.On("LoadBlockMeta", height).Return(nil)
	blockStore.On("LoadBlockCommit", height-1).Return(&types.Commit{})
	blockStore.On("LoadBlockMeta", height-1).Return(nil)

	_, _, err := state.Rollback(blockStore, stateStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "block at height 99 not found")
}

func TestRollbackDifferentStateHeight(t *testing.T) {
	const height = int64(100)
	stateStore := setupStateStore(t, height)
	blockStore := &mocks.BlockStore{}
	blockStore.On("Height").Return(height + 2)

	_, _, err := state.Rollback(blockStore, stateStore)
	require.Error(t, err)
	require.Equal(t, err.Error(), "statestore height (100) is not one below or equal to blockstore height (102)")
}

func setupStateStore(t *testing.T, height int64) state.Store {
	stateStore := state.NewStore(dbm.NewMemDB())
	valSet, _ := types.RandValidatorSet(5)

	params := types.DefaultConsensusParams()
	params.Version.AppVersion = 10

	initialState := state.State{
		Version: state.Version{
			Consensus: version.Consensus{
				Block: version.BlockProtocol,
				App:   10,
			},
			Software: version.TMCoreSemVer,
		},
		ChainID:                          factory.DefaultTestChainID,
		InitialHeight:                    10,
		LastBlockID:                      factory.MakeBlockID(),
		LastAppHash:                      factory.RandomHash(),
		LastResultsHash:                  factory.RandomHash(),
		LastBlockHeight:                  height,
		LastValidators:                   valSet,
		Validators:                       valSet.Copy(),
		LastHeightValidatorsChanged:      height + 1,
		ConsensusParams:                  *params,
		LastHeightConsensusParamsChanged: height + 1,
	}
	require.NoError(t, stateStore.Bootstrap(initialState))
	return stateStore
}
