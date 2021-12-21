package state_test

import (
	"bytes"
	"context"
	"github.com/tendermint/tendermint/internal/test/factory"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abciclient "github.com/tendermint/tendermint/abci/client"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/crypto/encoding"
	mmock "github.com/tendermint/tendermint/internal/mempool/mock"
	"github.com/tendermint/tendermint/internal/proxy"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/state/mocks"
	sf "github.com/tendermint/tendermint/internal/state/test/factory"
	"github.com/tendermint/tendermint/internal/store"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

var (
	chainID             = "execution_chain"
	testPartSize uint32 = 65536
)

func TestApplyBlock(t *testing.T) {
	app := &testApp{}
	cc := abciclient.NewLocalCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, _ := makeState(1, 1)
	// The state is local, so we just take the first proTxHash
	nodeProTxHash := &state.Validators.Validators[0].ProTxHash
	stateStore := sm.NewStore(stateDB)
	app.ValidatorSetUpdate = state.Validators.ABCIEquivalentValidatorUpdates()

	blockStore := store.NewBlockStore(dbm.NewMemDB())
	blockExec := sm.NewBlockExecutor(
		stateStore,
		log.TestingLogger(),
		proxyApp.Consensus(),
		proxyApp.Query(),
		mmock.Mempool{},
		sm.EmptyEvidencePool{},
		blockStore,
		nil,
	)

	block, err := sf.MakeBlock(state, 1, new(types.Commit), nil, 0)
	require.NoError(t, err)
	blockID := types.BlockID{
		Hash:          block.Hash(),
		PartSetHeader: block.MakePartSet(testPartSize).Header(),
	}

	state, err = blockExec.ApplyBlock(state, nodeProTxHash, blockID, block)
	require.NoError(t, err)

	// TODO check state and mempool
	assert.EqualValues(t, 1, state.Version.Consensus.App, "App version wasn't updated")
}

// TestBeginBlockByzantineValidators ensures we send byzantine validators list.
func TestBeginBlockByzantineValidators(t *testing.T) {
	app := &testApp{}
	cc := abciclient.NewLocalCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, privVals := makeState(1, 1)
	nodeProTxHash := &state.Validators.Validators[0].ProTxHash
	stateStore := sm.NewStore(stateDB)
	app.ValidatorSetUpdate = state.Validators.ABCIEquivalentValidatorUpdates()

	defaultEvidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	privVal := privVals[state.Validators.Validators[0].ProTxHash.String()]

	// we don't need to worry about validating the evidence as long as they pass validate basic
	dve, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		3,
		defaultEvidenceTime,
		privVal,
		state.ChainID,
		state.Validators.QuorumType,
		state.Validators.QuorumHash,
	)
	require.NoError(t, err)
	dve.ValidatorPower = types.DefaultDashVotingPower

	ev := []types.Evidence{dve}

	abciEv := []abci.Evidence{
		{
			Type:             abci.EvidenceType_DUPLICATE_VOTE,
			Height:           3,
			Time:             defaultEvidenceTime,
			Validator:        types.TM2PB.Validator(state.Validators.Validators[0]),
			TotalVotingPower: types.DefaultDashVotingPower,
		},
	}

	evpool := &mocks.EvidencePool{}
	evpool.On("PendingEvidence", mock.AnythingOfType("int64")).Return(ev, int64(100))
	evpool.On("Update", mock.AnythingOfType("state.State"), mock.AnythingOfType("types.EvidenceList")).
		Return()
	evpool.On("CheckEvidence", mock.AnythingOfType("types.EvidenceList")).Return(nil)

	blockStore := store.NewBlockStore(dbm.NewMemDB())

	blockExec := sm.NewBlockExecutor(
		stateStore,
		log.TestingLogger(),
		proxyApp.Consensus(),
		proxyApp.Query(),
		mmock.Mempool{},
		evpool,
		blockStore,
		nil,
	)

	block, err := sf.MakeBlock(state, 1, new(types.Commit), nil, 0)
	require.NoError(t, err)
	block.Evidence = types.EvidenceData{Evidence: ev}
	block.Header.EvidenceHash = block.Evidence.Hash()
	blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: block.MakePartSet(testPartSize).Header()}

	_, err = blockExec.ApplyBlock(state, nodeProTxHash, blockID, block)
	require.Nil(t, err)

	// TODO check state and mempool
	assert.Equal(t, abciEv, app.ByzantineValidators)
}

func TestValidateValidatorUpdates(t *testing.T) {
	pubkey1 := bls12381.GenPrivKey().PubKey()
	pubkey2 := bls12381.GenPrivKey().PubKey()
	pk1, err := encoding.PubKeyToProto(pubkey1)
	assert.NoError(t, err)
	pk2, err := encoding.PubKeyToProto(pubkey2)
	assert.NoError(t, err)

	proTxHash1 := crypto.RandProTxHash()
	proTxHash2 := crypto.RandProTxHash()

	defaultValidatorParams := types.ValidatorParams{
		PubKeyTypes: []string{types.ABCIPubKeyTypeBLS12381},
	}

	testCases := []struct {
		name string

		abciUpdates     []abci.ValidatorUpdate
		validatorParams types.ValidatorParams

		shouldErr bool
	}{
		{
			"adding a validator is OK",
			[]abci.ValidatorUpdate{{PubKey: &pk2, Power: 100, ProTxHash: proTxHash2}},
			defaultValidatorParams,
			false,
		},
		{
			"updating a validator is OK",
			[]abci.ValidatorUpdate{{PubKey: &pk1, Power: 100, ProTxHash: proTxHash1}},
			defaultValidatorParams,
			false,
		},
		{
			"removing a validator is OK",
			[]abci.ValidatorUpdate{{Power: 0, ProTxHash: proTxHash2}},
			defaultValidatorParams,
			false,
		},
		{
			"adding a validator with negative power results in error",
			[]abci.ValidatorUpdate{{PubKey: &pk2, Power: -100, ProTxHash: proTxHash2}},
			defaultValidatorParams,
			true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := sm.ValidateValidatorUpdates(tc.abciUpdates, tc.validatorParams)
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpdateValidators(t *testing.T) {
	validatorSet, _ := factory.RandValidatorSet(4)
	originalProTxHashes := validatorSet.GetProTxHashes()
	addedProTxHashes := bls12381.CreateProTxHashes(4)
	combinedProTxHashes := append(originalProTxHashes, addedProTxHashes...) // nolint:gocritic
	combinedValidatorSet, _ := factory.GenerateValidatorSetUsingProTxHashes(combinedProTxHashes)
	regeneratedValidatorSet, _ := factory.GenerateValidatorSetUsingProTxHashes(combinedProTxHashes)
	abciRegeneratedValidatorUpdates := regeneratedValidatorSet.ABCIEquivalentValidatorUpdates()
	removedProTxHashes := combinedValidatorSet.GetProTxHashes()[0 : len(combinedProTxHashes)-2] // these are sorted
	removedValidatorSet, _ := factory.GenerateValidatorSetUsingProTxHashes(
		removedProTxHashes,
	) // size 6
	abciRemovalValidatorUpdates := removedValidatorSet.ABCIEquivalentValidatorUpdates()
	abciRemovalValidatorUpdates.ValidatorUpdates = append(
		abciRemovalValidatorUpdates.ValidatorUpdates,
		abciRegeneratedValidatorUpdates.ValidatorUpdates[6:]...)
	abciRemovalValidatorUpdates.ValidatorUpdates[6].Power = 0
	abciRemovalValidatorUpdates.ValidatorUpdates[7].Power = 0

	pubkeyRemoval := bls12381.GenPrivKey().PubKey()
	pk, err := encoding.PubKeyToProto(pubkeyRemoval)
	require.NoError(t, err)

	testCases := []struct {
		name string

		currentSet               *types.ValidatorSet
		abciUpdates              *abci.ValidatorSetUpdate
		thresholdPublicKeyUpdate crypto.PubKey

		resultingSet *types.ValidatorSet
		shouldErr    bool
	}{
		{
			"adding a validator set is OK",
			validatorSet,
			combinedValidatorSet.ABCIEquivalentValidatorUpdates(),
			combinedValidatorSet.ThresholdPublicKey,
			combinedValidatorSet,
			false,
		},
		{
			"updating a validator set is OK",
			combinedValidatorSet,
			regeneratedValidatorSet.ABCIEquivalentValidatorUpdates(),
			regeneratedValidatorSet.ThresholdPublicKey,
			regeneratedValidatorSet,
			false,
		},
		{
			"removing a validator is OK",
			regeneratedValidatorSet,
			abciRemovalValidatorUpdates,
			removedValidatorSet.ThresholdPublicKey,
			removedValidatorSet,
			false,
		},
		{
			"removing a non-existing validator results in error",
			removedValidatorSet,
			&abci.ValidatorSetUpdate{
				ValidatorUpdates: []abci.ValidatorUpdate{
					{ProTxHash: crypto.RandProTxHash(), Power: 0},
				},
				ThresholdPublicKey: pk,
				QuorumHash:         removedValidatorSet.QuorumHash,
			},
			removedValidatorSet.ThresholdPublicKey,
			removedValidatorSet,
			true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			updates, _, _, err := types.PB2TM.ValidatorUpdatesFromValidatorSet(tc.abciUpdates)
			assert.NoError(t, err)
			err = tc.currentSet.UpdateWithChangeSet(
				updates,
				tc.thresholdPublicKeyUpdate,
				crypto.RandQuorumHash(),
			)
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				require.Equal(t, tc.resultingSet.Size(), tc.currentSet.Size())

				assert.Equal(t, tc.resultingSet.TotalVotingPower(), tc.currentSet.TotalVotingPower())

				assert.Equal(t, tc.resultingSet.Validators[0].ProTxHash, tc.currentSet.Validators[0].ProTxHash)
				if tc.resultingSet.Size() > 1 {
					assert.Equal(t, tc.resultingSet.Validators[1].ProTxHash, tc.currentSet.Validators[1].ProTxHash)
				}
			}
		})
	}
}

// TestEndBlockValidatorUpdates ensures we update validator set and send an event.
func TestEndBlockValidatorUpdates(t *testing.T) {
	app := &testApp{}
	cc := abciclient.NewLocalCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, _ := makeState(1, 1)
	nodeProTxHash := &state.Validators.Validators[0].ProTxHash
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	blockExec := sm.NewBlockExecutor(
		stateStore,
		log.TestingLogger(),
		proxyApp.Consensus(),
		proxyApp.Query(),
		mmock.Mempool{},
		sm.EmptyEvidencePool{},
		blockStore,
		nil,
	)

	eventBus := types.NewEventBus()
	err = eventBus.Start()
	require.NoError(t, err)
	defer eventBus.Stop() //nolint:errcheck // ignore for tests

	blockExec.SetEventBus(eventBus)

	updatesSub, err := eventBus.Subscribe(
		context.Background(),
		"TestEndBlockValidatorUpdates",
		types.EventQueryValidatorSetUpdates,
	)
	require.NoError(t, err)

	block, err := sf.MakeBlock(state, 1, new(types.Commit), nil, 0)
	require.NoError(t, err)

	blockID := types.BlockID{
		Hash:          block.Hash(),
		PartSetHeader: block.MakePartSet(testPartSize).Header(),
	}

	vals := state.Validators
	proTxHashes := vals.GetProTxHashes()
	addProTxHash := crypto.RandProTxHash()
	proTxHashes = append(proTxHashes, addProTxHash)
	newVals, _ := factory.GenerateValidatorSetUsingProTxHashes(proTxHashes)
	var pos int
	for i, proTxHash := range newVals.GetProTxHashes() {
		if bytes.Equal(proTxHash.Bytes(), addProTxHash.Bytes()) {
			pos = i
		}
	}

	app.ValidatorSetUpdate = newVals.ABCIEquivalentValidatorUpdates()

	state, err = blockExec.ApplyBlock(state, nodeProTxHash, blockID, block)
	require.Nil(t, err)
	// test new validator was added to NextValidators
	if assert.Equal(t, state.Validators.Size()+1, state.NextValidators.Size()) {
		idx, _ := state.NextValidators.GetByProTxHash(addProTxHash)
		if idx < 0 {
			t.Fatalf("can't find proTxHash %v in the set %v", addProTxHash, state.NextValidators)
		}
	}

	// test we threw an event
	select {
	case msg := <-updatesSub.Out():
		event, ok := msg.Data().(types.EventDataValidatorSetUpdate)
		require.True(
			t,
			ok,
			"Expected event of type EventDataValidatorSetUpdate, got %T",
			msg.Data(),
		)
		if assert.NotEmpty(t, event.ValidatorSetUpdates) {
			assert.Equal(t, addProTxHash, event.ValidatorSetUpdates[pos].ProTxHash)
			assert.EqualValues(
				t,
				types.DefaultDashVotingPower,
				event.ValidatorSetUpdates[1].VotingPower,
			)
		}
	case <-updatesSub.Canceled():
		t.Fatalf("updatesSub was canceled (reason: %v)", updatesSub.Err())
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive EventValidatorSetUpdates within 1 sec.")
	}
}

// TestEndBlockValidatorUpdatesResultingInEmptySet checks that processing validator updates that
// would result in empty set causes no panic, an error is raised and NextValidators is not updated
func TestEndBlockValidatorUpdatesResultingInEmptySet(t *testing.T) {
	app := &testApp{}
	cc := abciclient.NewLocalCreator(app)
	proxyApp := proxy.NewAppConns(cc)
	err := proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, _ := makeState(1, 1)
	nodeProTxHash := &state.Validators.Validators[0].ProTxHash
	stateStore := sm.NewStore(stateDB)
	proTxHash := state.Validators.Validators[0].ProTxHash
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	blockExec := sm.NewBlockExecutor(
		stateStore,
		log.TestingLogger(),
		proxyApp.Consensus(),
		proxyApp.Query(),
		mmock.Mempool{},
		sm.EmptyEvidencePool{},
		blockStore,
		nil,
	)

	block, err := sf.MakeBlock(state, 1, new(types.Commit), nil, 0)
	require.NoError(t, err)
	blockID := types.BlockID{
		Hash:          block.Hash(),
		PartSetHeader: block.MakePartSet(testPartSize).Header(),
	}

	publicKey, err := encoding.PubKeyToProto(bls12381.GenPrivKey().PubKey())
	require.NoError(t, err)
	// Remove the only validator
	validatorUpdates := []abci.ValidatorUpdate{
		{ProTxHash: proTxHash, Power: 0},
	}
	// the quorum hash needs to be the same
	// because we are providing an update removing a member from a known quorum, not changing the quorum
	app.ValidatorSetUpdate = &abci.ValidatorSetUpdate{
		ValidatorUpdates:   validatorUpdates,
		ThresholdPublicKey: publicKey,
		QuorumHash:         state.Validators.QuorumHash,
	}

	assert.NotPanics(
		t,
		func() { state, err = blockExec.ApplyBlock(state, nodeProTxHash, blockID, block) },
	)
	assert.NotNil(t, err)
	assert.NotEmpty(t, state.NextValidators.Validators)
}
