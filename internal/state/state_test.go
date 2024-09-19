package state_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	cryptoenc "github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/dash/llmq"
	"github.com/dashpay/tenderdash/internal/evidence/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
	"github.com/dashpay/tenderdash/types"
)

// setupTestCase does setup common to all test cases.
func setupTestCase(t *testing.T) (func(t *testing.T), dbm.DB, sm.State) {
	cfg, err := config.ResetTestRoot(t.TempDir(), "state_")
	require.NoError(t, err)

	dbType := dbm.BackendType(cfg.DBBackend)
	stateDB, err := dbm.NewDB("state", dbType, cfg.DBDir())
	require.NoError(t, err)
	stateStore := sm.NewStore(stateDB)
	state, err := stateStore.Load()
	require.NoError(t, err)
	require.Empty(t, state)
	state, err = sm.MakeGenesisStateFromFile(cfg.GenesisFile())
	assert.NoError(t, err)
	assert.NotNil(t, state)
	err = stateStore.Save(state)
	require.NoError(t, err)

	tearDown := func(_ *testing.T) { _ = os.RemoveAll(cfg.RootDir) }

	return tearDown, stateDB, state
}

// TestStateCopy tests the correct copying behavior of State.
func TestStateCopy(t *testing.T) {
	tearDown, _, state := setupTestCase(t)
	defer tearDown(t)

	stateCopy := state.Copy()

	seq, err := state.Equals(stateCopy)
	require.NoError(t, err)
	assert.True(t, seq,
		"expected state and its copy to be identical.\ngot: %v\nexpected: %v",
		stateCopy, state)

	stateCopy.LastBlockHeight++
	stateCopy.LastValidators = state.Validators

	seq, err = state.Equals(stateCopy)
	require.NoError(t, err)
	assert.False(t, seq, "expected states to be different. got same %v", state)
}

// TestMakeGenesisStateNilValidators tests state's consistency when genesis file's validators field is nil.
func TestMakeGenesisStateNilValidators(t *testing.T) {
	doc := types.GenesisDoc{
		ChainID:    "dummy",
		Validators: nil,
		QuorumType: btcjson.LLMQType_5_60,
	}
	require.Nil(t, doc.ValidateAndComplete())
	state, err := sm.MakeGenesisState(&doc)
	require.NoError(t, err)
	require.Equal(t, 0, len(state.Validators.Validators))
}

// TestStateSaveLoad tests saving and loading State from a db.
func TestStateSaveLoad(t *testing.T) {
	tearDown, stateDB, state := setupTestCase(t)
	defer tearDown(t)
	stateStore := sm.NewStore(stateDB)

	state.LastBlockHeight++
	state.LastValidators = state.Validators
	err := stateStore.Save(state)
	require.NoError(t, err)

	loadedState, err := stateStore.Load()
	require.NoError(t, err)
	seq, err := state.Equals(loadedState)
	require.NoError(t, err)
	assert.True(t, seq,
		"expected state and its copy to be identical.\ngot: %v\nexpected: %v",
		loadedState, state)
}

// TestFinalizeBlockResponsesSaveLoad1 tests saving and loading responses to FinalizeBlock.
func TestFinalizeBlockResponsesSaveLoad1(t *testing.T) {
	tearDown, stateDB, state := setupTestCase(t)
	defer tearDown(t)
	stateStore := sm.NewStore(stateDB)

	state.LastBlockHeight++

	// Build mock responses.
	block, err := statefactory.MakeBlock(state, 2, new(types.Commit), 0)
	require.NoError(t, err)

	var abciResponses tmstate.ABCIResponses
	dtxs := make([]*abci.ExecTxResult, 2)
	abciResponses.ProcessProposal = &abci.ResponseProcessProposal{
		Status:    abci.ResponseProcessProposal_ACCEPT,
		TxResults: dtxs,
	}

	abciResponses.ProcessProposal.TxResults[0] = &abci.ExecTxResult{Data: []byte("foo"), Events: nil}
	abciResponses.ProcessProposal.TxResults[1] = &abci.ExecTxResult{Data: []byte("bar"), Log: "ok", Events: nil}
	pubKey := bls12381.GenPrivKey().PubKey()
	abciPubKey, err := cryptoenc.PubKeyToProto(pubKey)
	require.NoError(t, err)

	vu := types.TM2PB.NewValidatorUpdate(pubKey, 100, crypto.RandProTxHash(), types.RandValidatorAddress().String())
	abciResponses.ProcessProposal.ValidatorSetUpdate = &abci.ValidatorSetUpdate{
		ValidatorUpdates:   []abci.ValidatorUpdate{vu},
		ThresholdPublicKey: abciPubKey,
	}

	err = stateStore.SaveABCIResponses(block.Height, abciResponses)
	require.NoError(t, err)
	loadedABCIResponses, err := stateStore.LoadABCIResponses(block.Height)
	require.NoError(t, err)
	assert.Equal(t, abciResponses, *loadedABCIResponses,
		"ABCIResponses don't match:\ngot:       %v\nexpected: %v\n",
		loadedABCIResponses, abciResponses)
}

// TestFinalizeBlockResponsesSaveLoad2 tests saving and loading responses to FinalizeBlock.
func TestFinalizeBlockResponsesSaveLoad2(t *testing.T) {
	tearDown, stateDB, _ := setupTestCase(t)
	defer tearDown(t)

	stateStore := sm.NewStore(stateDB)

	cases := [...]struct {
		// Height is implied to equal index+2,
		// as block 1 is created from genesis.
		added    []*abci.ExecTxResult
		expected []*abci.ExecTxResult
	}{
		0: {
			nil,
			nil,
		},
		1: {
			[]*abci.ExecTxResult{
				{Code: 32, Data: []byte("Hello"), Log: "Huh?"},
			},
			[]*abci.ExecTxResult{
				{Code: 32, Data: []byte("Hello")},
			},
		},
		2: {
			[]*abci.ExecTxResult{
				{Code: 383},
				{
					Data: []byte("Gotcha!"),
					Events: []abci.Event{
						{Type: "type1", Attributes: []abci.EventAttribute{{Key: "a", Value: "1"}}},
						{Type: "type2", Attributes: []abci.EventAttribute{{Key: "build", Value: "stuff"}}},
					},
				},
			},
			[]*abci.ExecTxResult{
				{Code: 383, Data: nil},
				{Code: 0, Data: []byte("Gotcha!"), Events: []abci.Event{
					{Type: "type1", Attributes: []abci.EventAttribute{{Key: "a", Value: "1"}}},
					{Type: "type2", Attributes: []abci.EventAttribute{{Key: "build", Value: "stuff"}}},
				}},
			},
		},
		3: {
			nil,
			nil,
		},
		4: {
			[]*abci.ExecTxResult{nil},
			nil,
		},
	}

	// Query all before, this should return error.
	for i := range cases {
		h := int64(i + 1)
		res, err := stateStore.LoadABCIResponses(h)
		assert.Error(t, err, "%d: %#v", i, res)
	}

	// Add all cases.
	for i, tc := range cases {
		h := int64(i + 1) // last block height, one below what we save
		responses := tmstate.ABCIResponses{
			ProcessProposal: &abci.ResponseProcessProposal{
				TxResults: tc.added,
				AppHash:   []byte("a_hash"),
				Status:    abci.ResponseProcessProposal_ACCEPT,
			},
		}
		err := stateStore.SaveABCIResponses(h, responses)
		require.NoError(t, err)
	}

	// Query all after, should return expected value.
	for i, tc := range cases {
		h := int64(i + 1)
		res, err := stateStore.LoadABCIResponses(h)
		if assert.NoError(t, err, "%d", i) {
			t.Log(res)
			e, err := abci.MarshalTxResults(tc.expected)
			require.NoError(t, err)
			he := merkle.HashFromByteSlices(e)
			rs, err := abci.MarshalTxResults(res.ProcessProposal.TxResults)
			hrs := merkle.HashFromByteSlices(rs)
			require.NoError(t, err)
			assert.Equal(t, he, hrs, "%d", i)
		}
	}
}

// TestValidatorSimpleSaveLoad tests saving and loading validators.
func TestValidatorSimpleSaveLoad(t *testing.T) {
	tearDown, stateDB, state := setupTestCase(t)
	defer tearDown(t)

	statestore := sm.NewStore(stateDB)
	blockStore := mocks.NewBlockStore(t)

	// Can't load anything for height 0.
	_, err := statestore.LoadValidators(0, blockStore)
	assert.IsType(t, sm.ErrNoValSetForHeight{}, err, "expected err at height 0")

	// Should be able to load for height 1.
	blockStore.On("Base").Return(int64(1))
	blockStore.On("LoadBlockMeta", int64(1)).Return(&types.BlockMeta{
		Header: types.Header{
			Height:            1,
			ProposerProTxHash: state.Validators.GetByIndex((int32(state.LastBlockHeight) % int32(state.Validators.Size()))).ProTxHash,
		}})

	v, err := statestore.LoadValidators(1, blockStore)
	require.NoError(t, err, "expected no err at height 1")
	assert.Equal(t, v.Hash(), state.Validators.Hash(), "expected validator hashes to match")

	// Should NOT be able to load for height 2.
	blockStore.On("LoadBlockMeta", int64(2)).Return(nil)
	_, err = statestore.LoadValidators(2, blockStore)
	require.Error(t, err, "expected no err at height 2")

	// Increment height, save; should be able to load for next height.
	state.LastBlockHeight++
	nextHeight := state.LastBlockHeight + 1
	err = statestore.Save(state)
	require.NoError(t, err)
	vp0, err := statestore.LoadValidators(nextHeight+0, blockStore)
	assert.NoError(t, err)
	assert.Equal(t, vp0.Hash(), state.Validators.Hash(), "expected validator hashes to match")

	_, err = statestore.LoadValidators(nextHeight+1, blockStore)
	assert.Error(t, err)
	// assert.Equal(t, vp1.Hash(), state.NextValidators.Hash(), "expected next validator hashes to match")
}

// TestValidatorChangesSaveLoad tests saving and loading a validator set with changes.
func TestOneValidatorChangesSaveLoad(t *testing.T) {
	tearDown, stateDB, state := setupTestCase(t)
	defer tearDown(t)
	stateStore := sm.NewStore(stateDB)
	blockStore := mocks.NewBlockStore(t)
	blockStore.On("Base").Return(int64(1))

	// Change vals at these heights.
	changeHeights := []int64{1, 2, 4, 5, 10, 15, 16, 17, 20}
	N := len(changeHeights)

	// Build the validator history by running updateState
	// with the right validator set for each height.
	highestHeight := changeHeights[N-1] + 5
	changeIndex := 0

	firstNode := state.Validators.GetByIndex(0)
	require.NotZero(t, firstNode.ProTxHash)

	ctx := dash.ContextWithProTxHash(context.Background(), firstNode.ProTxHash)
	keys := make([]crypto.PubKey, highestHeight+1)

	for height := int64(1); height < highestHeight; height++ {
		// When we get to a change height, use the next pubkey.
		regenerate := false
		if changeIndex < len(changeHeights) && height == changeHeights[changeIndex] {
			// after committing this height, we change keys, so next block (height + 1) will get new validator set
			changeIndex++
			regenerate = true
		}
		header, _, blockID, responses := makeHeaderPartsResponsesValKeysRegenerate(t, state, regenerate, 0)
		assert.EqualValues(t, height, header.Height)
		if regenerate {
			assert.NotEmpty(t, responses.ProcessProposal.ValidatorSetUpdate.ValidatorUpdates)
		} else {
			assert.Empty(t, responses.ProcessProposal.ValidatorSetUpdate)
		}

		changes, err := state.NewStateChangeset(
			ctx,
			sm.RoundParamsFromProcessProposal(responses.ProcessProposal, nil, 0),
		)
		require.NoError(t, err)

		state, err = state.Update(blockID, &header, &changes)

		blockStore.On("LoadBlockMeta", header.Height).Return(&types.BlockMeta{
			Header: header,
		}).Maybe()

		require.NoError(t, err)

		validator := state.Validators.Validators[0]
		keys[height+1] = validator.PubKey
		err = stateStore.Save(state)
		require.NoError(t, err)
	}

	for height := int64(2); height < highestHeight; height++ {
		pubKey := keys[height]                                  // new validators are in effect in the next block
		v, err := stateStore.LoadValidators(height, blockStore) // load validators that validate block at `height``
		require.NoError(t, err, fmt.Sprintf("expected no err at height %d", height))
		assert.Equal(t, 1, v.Size(), "validator set size is greater than 1: %d", v.Size())
		val := v.GetByIndex(0)

		assert.Equal(t, pubKey, val.PubKey, fmt.Sprintf(`unexpected pubKey at height %d`, height))
	}
}

// TestEmptyValidatorUpdates tests that the validator set is updated correctly when there are no validator updates.
func TestEmptyValidatorUpdates(t *testing.T) {
	tearDown, _, state := setupTestCase(t)
	defer tearDown(t)

	firstNode := state.Validators.GetByIndex(0)
	require.NotZero(t, firstNode.ProTxHash)
	ctx := dash.ContextWithProTxHash(context.Background(), firstNode.ProTxHash)

	newPrivKey := bls12381.GenPrivKeyFromSecret([]byte("test"))
	newPubKey := newPrivKey.PubKey()
	newQuorumHash := crypto.RandQuorumHash()

	expectValidators := types.ValidatorListString(state.Validators.Validators)

	resp := abci.ResponseProcessProposal{
		ValidatorSetUpdate: &abci.ValidatorSetUpdate{
			ValidatorUpdates:   nil,
			ThresholdPublicKey: cryptoenc.MustPubKeyToProto(newPubKey),
			QuorumHash:         newQuorumHash,
		}}

	changes, err := state.NewStateChangeset(
		ctx,
		sm.RoundParamsFromProcessProposal(&resp, nil, 0),
	)
	require.NoError(t, err)

	assert.EqualValues(t, newQuorumHash, changes.NextValidators.QuorumHash, "quorum hash should be updated")
	assert.EqualValues(t, newPubKey, changes.NextValidators.ThresholdPublicKey, "threshold public key should be updated")
	assert.Equal(t, expectValidators, types.ValidatorListString(changes.NextValidators.Validators), "validator should not change")
}

func TestFourAddFourMinusOneGenesisValidators(t *testing.T) {
	tearDown, _, state := setupTestCase(t)
	defer tearDown(t)

	originalValidatorSet, _ := types.RandValidatorSet(4)
	// reset state validators to above validator
	state.Validators = originalValidatorSet

	// Any node pro tx hash should do
	val0 := state.Validators.GetByIndex(0)
	ctx := dash.ContextWithProTxHash(context.Background(), val0.ProTxHash)

	execute := blockExecutorFunc(ctx, t)

	// All operations will be on same quorum hash
	quorumHash := crypto.RandQuorumHash()
	quorumHashOpt := abci.WithQuorumHash(quorumHash)

	// update state a few times with no validator updates
	// asserts that the single validator's ProposerPrio stays the same
	oldState := state
	for i := 0; i < 10; i++ {
		// no updates:
		changes, err := oldState.NewStateChangeset(ctx, sm.RoundParams{})
		assert.NoError(t, err)
		updatedState := execute(oldState, oldState, changes)
		// no changes in voting power (ProposerPrio += VotingPower == Voting in 1st round; than shiftByAvg == 0,
		// than -Total == -Voting)
		// -> no change in ProposerPrio (stays zero):
		assert.EqualValues(t, oldState.Validators.GetProTxHashesOrdered(), updatedState.Validators.GetProTxHashesOrdered())
		oldState = updatedState
	}

	addedProTxHashes := crypto.RandProTxHashes(4)
	proTxHashes := append(originalValidatorSet.GetProTxHashes(), addedProTxHashes...)
	abciValidatorUpdates0 := types.ValidatorUpdatesRegenerateOnProTxHashes(proTxHashes)
	ucState, err := state.NewStateChangeset(ctx, sm.RoundParams{ValidatorSetUpdate: &abciValidatorUpdates0})
	assert.NoError(t, err)
	updatedState := execute(state, state, ucState)

	lastState := updatedState
	for i := 0; i < 200; i++ {
		ucState, err = lastState.NewStateChangeset(ctx, sm.RoundParams{ValidatorSetUpdate: &abciValidatorUpdates0})
		assert.NoError(t, err)
		lastState = execute(lastState, lastState, ucState)
	}
	// set state to last state of above iteration
	state = lastState

	// set oldState to state before above iteration
	oldState = updatedState

	// we will keep the same quorum hash as to be able to add validators

	// add 10 validators with the same voting power as the one added directly after genesis:

	for i := 0; i < 5; i++ {
		ld := llmq.MustGenerate(append(proTxHashes, crypto.RandProTxHash()))
		abciValidatorSetUpdate, err := abci.LLMQToValidatorSetProto(*ld, quorumHashOpt)
		require.NoError(t, err)

		ucState, err := state.NewStateChangeset(ctx, sm.RoundParams{ValidatorSetUpdate: abciValidatorSetUpdate})
		assert.NoError(t, err)
		state = execute(oldState, state, ucState)
		assertLLMQDataWithValidatorSet(t, ld, state.Validators)
		proTxHashes = ld.ProTxHashes
	}

	ld := llmq.MustGenerate(append(proTxHashes, crypto.RandProTxHashes(5)...))
	abciValidatorSetUpdate, err := abci.LLMQToValidatorSetProto(*ld, quorumHashOpt)
	require.NoError(t, err)

	ucState, err = state.NewStateChangeset(ctx, sm.RoundParams{ValidatorSetUpdate: abciValidatorSetUpdate})
	require.NoError(t, err)
	state = execute(oldState, state, ucState)
	assertLLMQDataWithValidatorSet(t, ld, state.Validators)

	abciValidatorSetUpdate.ValidatorUpdates[0] = abci.ValidatorUpdate{ProTxHash: ld.ProTxHashes[0]}
	ucState, err = state.NewStateChangeset(ctx, sm.RoundParams{ValidatorSetUpdate: abciValidatorSetUpdate})
	require.NoError(t, err)
	updatedState = execute(oldState, state, ucState)

	// only the first added val (not the genesis val) should be left
	ld.ProTxHashes = ld.ProTxHashes[1:]
	assertLLMQDataWithValidatorSet(t, ld, updatedState.Validators)

	abciValidatorSetUpdate.ValidatorUpdates = []abci.ValidatorUpdate{
		{ProTxHash: ld.ProTxHashes[0]},
		{ProTxHash: ld.ProTxHashes[1]},
	}
	ucState, err = updatedState.NewStateChangeset(ctx, sm.RoundParams{ValidatorSetUpdate: abciValidatorSetUpdate})
	require.NoError(t, err)
	updatedState = execute(state, updatedState, ucState)

	// the second and third should be left
	ld.ProTxHashes = ld.ProTxHashes[2:]
	assertLLMQDataWithValidatorSet(t, ld, updatedState.Validators)
	changes, err := updatedState.NewStateChangeset(ctx, sm.RoundParams{})
	require.NoError(t, err)
	updatedState = execute(updatedState, updatedState, changes)
	// store proposers here to see if we see them again in the same order:
	numVals := len(updatedState.Validators.Validators)
	proposers := make([]*types.Validator, numVals)
	for i := 0; i < 100; i++ {
		changes, err = updatedState.NewStateChangeset(ctx, sm.RoundParams{})
		assert.NoError(t, err)
		updatedState = execute(state, updatedState, changes)
		if i > numVals { // expect proposers to cycle through after the first iteration (of numVals blocks):
			if proposers[i%numVals] == nil {
				proposers[i%numVals] = updatedState.Validators.Proposer()
			} else {
				assert.Equal(t, proposers[i%numVals], updatedState.Validators.Proposer())
			}
		}
	}
}

// TestValidatorChangesSaveLoad tests saving and loading a validator set with
// changes.
func TestManyValidatorChangesSaveLoad(t *testing.T) {
	const valSetSize = 7
	blockStore := mocks.NewBlockStore(t)

	// ====== GENESIS STATE, height 1 ====== //

	tearDown, stateDB, state := setupTestCase(t)
	defer tearDown(t)
	stateStore := sm.NewStore(stateDB)
	require.Equal(t, int64(0), state.LastBlockHeight)
	state.Validators, _ = types.RandValidatorSet(valSetSize)
	err := stateStore.Save(state)
	require.NoError(t, err)

	blockStore.On("LoadBlockMeta", state.LastBlockHeight).Return(&types.BlockMeta{
		Header: types.Header{
			Height:            state.LastBlockHeight,
			ProposerProTxHash: state.Validators.GetByIndex(0).ProTxHash,
		}}).Maybe()
	blockStore.On("Base").Return(state.LastBlockHeight)

	// ====== HEIGHT 2 ====== //

	// We receive new validator set, which will be used at height 3

	// First, we create proposal...
	val0 := state.Validators.GetByIndex(0)
	ctx := dash.ContextWithProTxHash(context.Background(), val0.ProTxHash)
	proTxHash := val0.ProTxHash // this is not really old, as it stays the same
	oldPubkey := val0.PubKey

	// Swap the first validator with a new one (validator set size stays the same).
	header, coreChainLock, blockID, responses := makeHeaderPartsResponsesValKeysRegenerate(t, state, true, 0)
	currentHeight := header.Height + 1
	assert.Equal(t, int64(2), currentHeight)

	// retrieve pubKey to use in assertions below
	var newPubkey crypto.PubKey
	for _, valUpdate := range responses.ProcessProposal.ValidatorSetUpdate.ValidatorUpdates {
		if proTxHash.Equal(valUpdate.ProTxHash) {
			newPubkey, err = cryptoenc.PubKeyFromProto(*valUpdate.PubKey)
			require.NoError(t, err)
		}
	}
	rp := sm.RoundParamsFromProcessProposal(responses.ProcessProposal, coreChainLock, 0)
	// Prepare state to generate height 2
	changes, err := state.NewStateChangeset(ctx, rp)
	require.NoError(t, err)

	state, err = state.Update(blockID, &header, &changes)
	require.NoError(t, err)
	assert.Equal(t, currentHeight-1, state.LastBlockHeight)

	err = stateStore.Save(state)
	require.NoError(t, err)
	blockStore.On("LoadBlockMeta", currentHeight).Return(&types.BlockMeta{
		Header: types.Header{
			Height:            currentHeight,
			ProposerProTxHash: state.Validators.GetByIndex(int32(currentHeight-state.InitialHeight) % valSetSize).ProTxHash,
		}}).Maybe()

	// Load height, it should be the oldpubkey.
	v0, err := stateStore.LoadValidators(currentHeight-1, blockStore)
	assert.NoError(t, err)
	assert.Equal(t, valSetSize, v0.Size())
	index, val := v0.GetByProTxHash(proTxHash)
	assert.NotNil(t, val)
	assert.Equal(t, val.PubKey, oldPubkey, "the public key should match the old public key")
	if index < 0 {
		t.Fatal("expected to find old validator")
	}

	// Load nextheight+1, it should be the new pubkey.
	v1, err := stateStore.LoadValidators(currentHeight, blockStore)
	require.NoError(t, err)
	assert.Equal(t, valSetSize, v1.Size())
	index, val = v1.GetByProTxHash(proTxHash)
	assert.NotNil(t, val)
	assert.NotEqual(t, val.PubKey, oldPubkey, "regenerated public key expected")
	assert.Equal(t, val.PubKey, newPubkey, "the public key should match the regenerated public key")
	if index < 0 {
		t.Fatal("expected to find same validator by new address")
	}
}

func TestStateMakeBlock(t *testing.T) {
	tearDown, _, state := setupTestCase(t)
	defer tearDown(t)

	stateVersion := state.Version.Consensus
	// temporary workaround; state.Version.Consensus is deprecated and will be removed
	stateVersion.App = kvstore.ProtocolVersion
	var height int64 = 2
	state.LastBlockHeight = height - 1
	proposerProTxHash := state.GetProposerFromState(height, 0).ProTxHash
	block, err := statefactory.MakeBlock(state, height, new(types.Commit), 0)
	require.NoError(t, err)

	// test we set some fields
	assert.Equal(t, stateVersion, block.Version)
	assert.Equal(t, proposerProTxHash, block.ProposerProTxHash)
}

// TestConsensusParamsChangesSaveLoad tests saving and loading consensus params
// with changes.
func TestConsensusParamsChangesSaveLoad(t *testing.T) {
	tearDown, stateDB, state := setupTestCase(t)
	defer tearDown(t)

	stateStore := sm.NewStore(stateDB)

	// Change vals at these heights.
	changeHeights := []int64{1, 2, 4, 5, 10, 15, 16, 17, 20}
	N := len(changeHeights)

	// Build the params history by running updateState
	// with the right params set for each height.
	highestHeight := changeHeights[N-1] + 5
	changeIndex := 0

	// Each valset is just one validator.
	// create list of them.
	params := make([]types.ConsensusParams, N+1)
	params[0] = state.ConsensusParams
	for i := 1; i < N+1; i++ {
		params[i] = *types.DefaultConsensusParams()
		params[i].Block.MaxBytes += int64(i)
	}

	cp := params[changeIndex]
	for height := int64(1); height < highestHeight; height++ {
		// When we get to a change height, use the next params.
		if changeIndex < len(changeHeights) && height == changeHeights[changeIndex] {
			changeIndex++
			cp = params[changeIndex]
		}

		header, coreChainLock, blockID, responses := makeHeaderPartsResponsesParams(t, state, &cp, 0)

		// Any node pro tx hash should do
		firstNode := state.Validators.GetByIndex(0)
		ctx := dash.ContextWithProTxHash(context.Background(), firstNode.ProTxHash)

		rp := sm.RoundParamsFromProcessProposal(responses.ProcessProposal, coreChainLock, 0)
		changes, err := state.NewStateChangeset(ctx, rp)
		require.NoError(t, err)
		state, err = state.Update(blockID, &header, &changes)

		require.NoError(t, err)
		err = stateStore.Save(state)
		require.NoError(t, err)
	}

	// Make all the test cases by using the same params until after the change.
	testCases := make([]paramsChangeTestCase, highestHeight)
	changeIndex = 0
	cp = params[changeIndex]
	for i := int64(1); i < highestHeight+1; i++ {
		// We get to the height after a change height use the next pubkey (note
		// our counter starts at 0 this time).
		if changeIndex < len(changeHeights) && i == changeHeights[changeIndex]+1 {
			changeIndex++
			cp = params[changeIndex]
		}
		testCases[i-1] = paramsChangeTestCase{i, cp}
	}

	for _, testCase := range testCases {
		p, err := stateStore.LoadConsensusParams(testCase.height)

		assert.NoError(t, err, fmt.Sprintf("expected no err at height %d", testCase.height))
		assert.EqualValues(t, testCase.params, p, fmt.Sprintf(`unexpected consensus params at
                height %d`, testCase.height))
	}
}

func TestStateProto(t *testing.T) {
	tearDown, _, state := setupTestCase(t)
	defer tearDown(t)

	tc := []struct {
		testName string
		state    *sm.State
		expPass1 bool
		expPass2 bool
	}{
		{"empty state", &sm.State{}, true, false},
		{"nil failure state", nil, false, false},
		{"success state", &state, true, true},
	}

	for _, tt := range tc {
		tt := tt
		t.Run(tt.testName, func(t *testing.T) {
			pbs, err := tt.state.ToProto()
			if !tt.expPass1 {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err, tt.testName)
			}

			smt, err := sm.FromProto(pbs)
			if tt.expPass2 {
				require.NoError(t, err, tt.testName)
				require.Equal(t, tt.state, smt, tt.testName)
			} else {
				require.Error(t, err, tt.testName)
			}
		})
	}
}

func blockExecutorFunc(_ctx context.Context, t *testing.T) func(prevState, state sm.State, ucState sm.CurrentRoundState) sm.State {
	return func(prevState, state sm.State, ucState sm.CurrentRoundState) sm.State {
		t.Helper()

		block, err := statefactory.MakeBlock(prevState, prevState.LastBlockHeight+1, new(types.Commit), 0)
		require.NoError(t, err)
		blockID := block.BlockID(nil)
		require.NoError(t, err)

		state, err = state.Update(blockID, &block.Header, &ucState)
		require.NoError(t, err)
		return state
	}
}

func assertLLMQDataWithValidatorSet(t *testing.T, ld *llmq.Data, valSet *types.ValidatorSet) {
	t.Helper()
	require.Equal(t, len(ld.ProTxHashes), len(valSet.Validators), "valset and proTxHash len mismatch")
	m := make(map[string]struct{})
	for _, proTxHash := range ld.ProTxHashes {
		m[proTxHash.String()] = struct{}{}
	}
	for _, val := range valSet.Validators {
		_, ok := m[val.ProTxHash.String()]
		require.True(t, ok)
	}
	require.Equal(t, ld.ThresholdPubKey, valSet.ThresholdPublicKey)
}
