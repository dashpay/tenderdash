package state_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	"github.com/dashpay/tenderdash/internal/evidence/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
	"github.com/dashpay/tenderdash/types"
)

const (
	// make sure this is the same as in state/store.go
	valSetCheckpointInterval = 100000
)

// mockBlockStoreForProposerSelector creates a mock block store that returns proposers based on the height.
// It assumes every block ends in round 0 and the proposer is the next validator in the validator set.
func mockBlockStoreForProposerSelector(t *testing.T, startHeight, endHeight int64, vals *types.ValidatorSet) selectproposer.BlockStore {
	blockStore := mocks.NewBlockStore(t)
	blockStore.On("Base").Return(startHeight).Maybe()
	configureBlockMetaWithValidators(t, blockStore, startHeight, endHeight, vals)

	return blockStore
}

// configureBlockMetaWithValidators configures the block store to return proposers based on the height.
func configureBlockMetaWithValidators(_ *testing.T, blockStore *mocks.BlockStore, startHeight, endHeight int64, vals *types.ValidatorSet) {
	vals = vals.Copy()
	valsHash := vals.Hash()

	for h := startHeight; h <= endHeight; h++ {
		blockStore.On("LoadBlockMeta", h).
			Return(&types.BlockMeta{
				Header: types.Header{
					Height:             h,
					ProposerProTxHash:  vals.Proposer().ProTxHash,
					ValidatorsHash:     valsHash,
					NextValidatorsHash: valsHash,
				},
			}).Maybe()
		vals.IncProposerIndex(1)
	}
}

func TestStoreBootstrap(t *testing.T) {
	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	vals, _ := types.RandValidatorSet(3)

	blockStore := mockBlockStoreForProposerSelector(t, 99, 100, vals)

	bootstrapState := makeRandomStateFromValidatorSet(vals, 100, 100, blockStore)
	require.NoError(t, stateStore.Bootstrap(bootstrapState))

	// bootstrap should also save the previous validator
	_, err := stateStore.LoadValidators(99, blockStore)
	require.NoError(t, err)

	_, err = stateStore.LoadValidators(100, blockStore)
	require.NoError(t, err)

	_, err = stateStore.LoadValidators(101, blockStore)
	require.Error(t, err)

	state, err := stateStore.Load()
	require.NoError(t, err)
	require.Equal(t, bootstrapState, state)
}

// assertProposer checks if the proposer at height h is correct (assuming no rounds and we started at initial height 1)
func assertProposer(t *testing.T, valSet *types.ValidatorSet, h int64) {
	t.Helper()

	const initialHeight = 1

	// check if currently selected proposer is correct
	idx, _ := valSet.GetByProTxHash(valSet.Proposer().ProTxHash)
	exp := (h - initialHeight) % int64(valSet.Size())
	assert.EqualValues(t, exp, idx, "pre-set proposer at height %d", h)

	// check if GetProposer returns the same proposer
	vs, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, valSet.Copy(), h, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	prop := vs.MustGetProposer(h, 0)
	idx, _ = valSet.GetByProTxHash(prop.ProTxHash)
	assert.EqualValues(t, exp, idx, "strategy-generated proposer at height %d", h)
}

func TestStoreLoadValidators(t *testing.T) {
	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	vals, _ := types.RandValidatorSet(3)

	expectedVS, err := selectproposer.NewProposerSelector(types.ConsensusParams{}, vals.Copy(), 1, 0, nil, log.NewTestingLogger(t))
	require.NoError(t, err)

	// initialize block store - create mock validators for each height
	blockStoreVS := expectedVS.Copy()
	blockStore := mockBlockStoreForProposerSelector(t, 1, valSetCheckpointInterval, blockStoreVS.ValidatorSet())

	// 1) LoadValidators loads validators using a height where they were last changed
	// Note that only the current validators at height h are saved
	require.NoError(t, stateStore.Save(makeRandomStateFromValidatorSet(vals, 1, 1, blockStore)))

	require.NoError(t, stateStore.Save(makeRandomStateFromValidatorSet(vals, 2, 1, blockStore)))

	loadedValsH1, err := stateStore.LoadValidators(1, blockStore)
	require.NoError(t, err)
	assertProposer(t, loadedValsH1, 1)

	loadedValsH2, err := stateStore.LoadValidators(2, blockStore)
	require.NoError(t, err)
	assertProposer(t, loadedValsH2, 2)

	_, err = stateStore.LoadValidators(3, blockStore)
	assert.Error(t, err, "no validator expected at this height")

	err = expectedVS.UpdateHeightRound(2, 0)
	require.NoError(t, err)
	assertProposer(t, expectedVS.ValidatorSet(), 2)

	require.Equal(t, expectedVS.ValidatorSet(), loadedValsH2)

	// 2) LoadValidators loads validators using a checkpoint height

	// add a validator set after the checkpoint
	state := makeRandomStateFromValidatorSet(vals, valSetCheckpointInterval+1, 1, nil)
	err = stateStore.Save(state)
	require.NoError(t, err)

	// check that a request will go back to the last checkpoint
	_, err = stateStore.LoadValidators(valSetCheckpointInterval+1, blockStore)
	require.Error(t, err)
	require.Equal(t, fmt.Sprintf("couldn't find validators at height %d (height %d was originally requested): "+
		"value retrieved from db is empty",
		valSetCheckpointInterval, valSetCheckpointInterval+1), err.Error())

	// now save a validator set at that checkpoint
	err = stateStore.Save(makeRandomStateFromValidatorSet(vals, valSetCheckpointInterval, 1, blockStore))
	require.NoError(t, err)

	valsAtCheckpoint, err := stateStore.LoadValidators(valSetCheckpointInterval, blockStore)
	require.NoError(t, err)

	// ensure we have correct validator set loaded; at height h, we expcect `(h+1) % 3`
	// (adding 1 as we start from initial height 1).
	for h := int64(2); h <= valSetCheckpointInterval-1; h++ {
		require.NoError(t, expectedVS.UpdateHeightRound(h, 0))
	}
	expected := expectedVS.ValidatorSet()
	assertProposer(t, expected, valSetCheckpointInterval-1)
	require.NotEqual(t, expected, valsAtCheckpoint)

	require.NoError(t, expectedVS.UpdateHeightRound(valSetCheckpointInterval, 0))
	expected = expectedVS.ValidatorSet()
	assertProposer(t, expected, valSetCheckpointInterval)
	require.Equal(t, expected, valsAtCheckpoint)
}

// Given a set of blocks in the block store and two validator sets that rotate,
// When we load the validators during quorum rotation,
// Then we receive the correct validators for each height.
func TestStoreLoadValidatorsOnRotation(t *testing.T) {
	const startHeight = int64(1)

	testCases := []struct {
		rotations        int64
		nVals            int
		nValSets         int64
		consensusVersion int32
	}{
		{1, 3, 3, 0},
		{1, 3, 3, 1},
		{2, 3, 2, 0},
		{2, 3, 2, 1},
		{3, 2, 4, 0},
		{3, 2, 4, 1},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("rotations=%d,nVals=%d,nValSets=%d,ver=%d",
			tc.rotations, tc.nVals, tc.nValSets, tc.consensusVersion), func(t *testing.T) {
			rotations := tc.rotations
			nVals := tc.nVals
			nValSets := tc.nValSets
			uncommittedHeight := startHeight + rotations*int64(nVals)

			stateDB := dbm.NewMemDB()
			stateStore := sm.NewStore(stateDB, log.NewTestingLoggerWithLevel(t, log.LogLevelDebug))

			validators := make([]*types.ValidatorSet, nValSets)
			for i := int64(0); i < nValSets; i++ {
				validators[i], _ = types.RandValidatorSet(nVals)
				t.Logf("Validator set %d: %v", i, validators[i].Hash())
			}

			blockStore := mocks.NewBlockStore(t)
			blockStore.On("Base").Return(startHeight).Maybe()

			// configure block store and state store to return correct validators and block meta
			for i := int64(0); i < rotations; i++ {
				nextQuorumHeight := startHeight + (i+1)*int64(nVals)

				err := stateStore.SaveValidatorSets(startHeight+i*int64(nVals), nextQuorumHeight-1, validators[i%nValSets])
				require.NoError(t, err)

				vals := validators[i%nValSets].Copy()
				// reset proposer
				require.NoError(t, vals.SetProposer(vals.GetByIndex(0).ProTxHash))
				valsHash := vals.Hash()
				nextValsHash := valsHash // we only change it in last block

				// configure block store to return correct validator sets and proposers
				for h := startHeight + i*int64(nVals); h < nextQuorumHeight; h++ {
					if h == nextQuorumHeight-1 {
						nextValsHash = validators[(i+1)%nValSets].Hash()
					}
					blockStore.On("LoadBlockMeta", h).
						Return(&types.BlockMeta{
							Header: types.Header{
								Height:             h,
								ProposerProTxHash:  vals.Proposer().ProTxHash,
								ValidatorsHash:     valsHash,
								NextValidatorsHash: nextValsHash,
							},
						}).Maybe()
					vals.IncProposerIndex(1)

					// set consensus version
					state := makeRandomStateFromValidatorSet(vals, h+1, startHeight+i*int64(nVals), blockStore)
					state.LastHeightConsensusParamsChanged = h + 1
					state.ConsensusParams.Version.ConsensusVersion = tc.consensusVersion
					require.NoError(t, stateStore.Save(state))
				}

				assert.LessOrEqual(t, nextQuorumHeight, uncommittedHeight, "we should not save block at height {}", uncommittedHeight)
			}

			// now, we are at height 10, and we should rotate to next validators
			// we don't have the last block yet, but we already have validator set for the next height
			blockStore.On("LoadBlockMeta", uncommittedHeight).Return(nil).Maybe()

			expectedValidators := validators[rotations%nValSets]
			err := stateStore.SaveValidatorSets(uncommittedHeight, uncommittedHeight, expectedValidators)
			require.NoError(t, err)

			// We should get correct validator set from the store
			readVals, err := stateStore.LoadValidators(uncommittedHeight, blockStore)
			require.NoError(t, err)
			assert.Equal(t, expectedValidators, readVals)
		})
	}
}

// This benchmarks the speed of loading validators from different heights if there is no validator set change.
// NOTE: This isn't too indicative of validator retrieval speed as the db is always (regardless of height) only
// performing two operations: 1) retrieve validator info at height x, which has a last validator set change of 1
// and 2) retrieve the validator set at the aforementioned height 1.
func BenchmarkLoadValidators(b *testing.B) {
	const valSetSize = 100
	blockStore := mocks.NewBlockStore(b)
	blockStore.On("LoadBlockCommit", mock.Anything).Return(&types.Commit{})

	cfg, err := config.ResetTestRoot(b.TempDir(), "state_")
	require.NoError(b, err)

	defer os.RemoveAll(cfg.RootDir)
	dbType := dbm.BackendType(cfg.DBBackend)
	stateDB, err := dbm.NewDB("state", dbType, cfg.DBDir())
	require.NoError(b, err)
	stateStore := sm.NewStore(stateDB)
	state, err := sm.MakeGenesisStateFromFile(cfg.GenesisFile())
	if err != nil {
		b.Fatal(err)
	}

	state.Validators, _ = types.RandValidatorSet(valSetSize)
	err = stateStore.Save(state)
	require.NoError(b, err)

	b.ResetTimer()

	for i := 10; i < 10000000000; i *= 10 { // 10, 100, 1000, ...
		i := i
		err = stateStore.Save(makeRandomStateFromValidatorSet(state.Validators,
			int64(i)-1, state.LastHeightValidatorsChanged, blockStore))
		if err != nil {
			b.Fatalf("error saving store: %v", err)
		}

		b.Run(fmt.Sprintf("height=%d", i), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				_, err := stateStore.LoadValidators(int64(i), blockStore)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestStoreLoadConsensusParams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	err := stateStore.Save(makeRandomStateFromConsensusParams(ctx, t, types.DefaultConsensusParams(), 1, 1))
	require.NoError(t, err)
	params, err := stateStore.LoadConsensusParams(1)
	require.NoError(t, err)
	require.Equal(t, types.DefaultConsensusParams(), &params)

	// we give the state store different params but say that the height hasn't changed, hence
	// it should save a pointer to the params at height 1
	differentParams := types.DefaultConsensusParams()
	differentParams.Block.MaxBytes = 20000
	err = stateStore.Save(makeRandomStateFromConsensusParams(ctx, t, differentParams, 10, 1))
	require.NoError(t, err)
	res, err := stateStore.LoadConsensusParams(10)
	require.NoError(t, err)
	require.Equal(t, res, params)
	require.NotEqual(t, res, differentParams)
}

func TestPruneStates(t *testing.T) {
	testcases := map[string]struct {
		startHeight           int64
		endHeight             int64
		pruneHeight           int64
		expectErr             bool
		remainingValSetHeight int64
		remainingParamsHeight int64
	}{
		"error when prune height is 0":           {1, 100, 0, true, 0, 0},
		"error when prune height is negative":    {1, 100, -10, true, 0, 0},
		"error when prune height does not exist": {1, 100, 101, true, 0, 0},
		"prune all":                              {1, 100, 100, false, 93, 95},
		"prune from non 1 height":                {10, 50, 40, false, 33, 35},
		"prune some":                             {1, 10, 8, false, 3, 5},
		// we test this because we flush to disk every 1000 "states"
		"prune more than 1000 state": {1, 1010, 1010, false, 1003, 1005},
		"prune across checkpoint":    {99900, 100002, 100002, false, 100000, 99995},
	}
	for name, tc := range testcases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			db := dbm.NewMemDB()

			stateStore := sm.NewStore(db)
			pk := bls12381.GenPrivKey().PubKey()

			proTxHash := crypto.RandProTxHash()

			// Generate a bunch of state data. Validators change for heights ending with 3, and
			// parameters when ending with 5.
			validator := &types.Validator{VotingPower: types.DefaultDashVotingPower, PubKey: pk, ProTxHash: proTxHash}
			validatorSet := &types.ValidatorSet{
				Validators:         []*types.Validator{validator},
				ThresholdPublicKey: validator.PubKey,
				QuorumHash:         crypto.RandQuorumHash(),
			}
			valsChanged := int64(0)
			paramsChanged := int64(0)

			for h := tc.startHeight; h <= tc.endHeight; h++ {
				if valsChanged == 0 || h%10 == 3 {
					valsChanged = h
				}
				if paramsChanged == 0 || h%10 == 5 {
					paramsChanged = h
				}

				state := sm.State{
					InitialHeight:   1,
					LastBlockHeight: h - 1,
					Validators:      validatorSet,
					ConsensusParams: types.ConsensusParams{
						Block: types.BlockParams{MaxBytes: 10e6},
					},
					LastHeightValidatorsChanged:      valsChanged,
					LastHeightConsensusParamsChanged: paramsChanged,
				}

				if state.LastBlockHeight >= 1 {
					state.LastValidators = state.Validators
				}

				err := stateStore.Save(state)
				require.NoError(t, err)

				err = stateStore.SaveABCIResponses(h, tmstate.ABCIResponses{
					ProcessProposal: &abci.ResponseProcessProposal{
						TxResults: []*abci.ExecTxResult{
							{Data: []byte{1}},
							{Data: []byte{2}},
							{Data: []byte{3}},
						},
					},
				})
				require.NoError(t, err)
			}

			// Test assertions
			err := stateStore.PruneStates(tc.pruneHeight)
			if tc.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			blockStore := mockBlockStoreForProposerSelector(t, tc.remainingValSetHeight, tc.endHeight, validatorSet)
			// We initialize block store from remainingValSetHeight just to pass this test; in practive, it can be
			// pruned. But here we want to check state store logic, not block store logic.
			// for h := int64(1); h < tc.remainingValSetHeight; h++ {
			// 	blockStore.On("LoadBlockMeta", h).Return(nil).Maybe()
			// }

			for h := tc.pruneHeight; h <= tc.endHeight; h++ {
				vals, err := stateStore.LoadValidators(h, blockStore)
				require.NoError(t, err, h)
				require.NotNil(t, vals, h)

				params, err := stateStore.LoadConsensusParams(h)
				require.NoError(t, err, h)
				require.NotNil(t, params, h)

				abciRes, err := stateStore.LoadABCIResponses(h)
				require.NoError(t, err, h)
				require.NotNil(t, abciRes, h)
			}

			emptyParams := types.ConsensusParams{}

			for h := tc.startHeight; h < tc.pruneHeight; h++ {
				vals, err := stateStore.LoadValidators(h, blockStore)
				if h == tc.remainingValSetHeight {
					require.NoError(t, err, h)
					require.NotNil(t, vals, h)
				} else {
					require.Error(t, err, h)
					require.Nil(t, vals, h)
				}

				params, err := stateStore.LoadConsensusParams(h)
				if h == tc.remainingParamsHeight {
					require.NoError(t, err, h)
					require.NotEqual(t, emptyParams, params, h)
				} else {
					require.Error(t, err, h)
					require.Equal(t, emptyParams, params, h)
				}

				abciRes, err := stateStore.LoadABCIResponses(h)
				require.Error(t, err, h)
				require.Nil(t, abciRes, h)
			}
		})
	}
}
